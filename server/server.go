package server

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aherve/giflichess/gifmaker"
	"github.com/aherve/giflichess/lichess"
	"github.com/notnil/chess"
)

//go:embed templates/index.gohtml templates/partials/nav.gohtml templates/partials/footer.gohtml
var homeTemplates embed.FS

func staticDir() string {
	d := os.Getenv("GIFCHESS_STATIC_DIR")
	if d != "" {
		return d
	}
	return "./static"
}

// Serve starts a server
func Serve(port, maxConcurrency int) {
	tpl, err := template.ParseFS(homeTemplates,
		"templates/index.gohtml",
		"templates/partials/nav.gohtml",
		"templates/partials/footer.gohtml",
	)
	if err != nil {
		log.Fatal(err)
	}

	staticRoot := staticDir()
	fileServer := http.FileServer(http.Dir(staticRoot))
	log.Printf("static files from %s\n", staticRoot)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", pingHandler)
	mux.HandleFunc("/api/lichess/", lichessGifHandler(maxConcurrency))
	mux.HandleFunc("/api/pgn", pgnHandler(maxConcurrency))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			if err := tpl.ExecuteTemplate(w, "home", nil); err != nil {
				log.Printf("home template: %v", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		p := r.URL.Path
		if strings.HasSuffix(p, ".html") {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		fileServer.ServeHTTP(w, r)
	})

	log.Printf("starting %s server on port %v with concurrency=%v\n", env(), port, maxConcurrency)
	log.Printf("Web UI available at: http://localhost:%v\n", port)
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(port), mux))
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte("{\"ping\": \"pong\"}"))
	log := func() {
		log.Println(r.Method, r.URL, 200, time.Since(start))
	}
	defer log()
}

func lichessGifHandler(maxConcurrency int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		start := time.Now()
		var status int
		log := func() {
			log.Println(r.Method, r.URL, status, time.Since(start))
		}
		defer log()

		// get ID
		maybeID, err := getIDFromQuery(r)
		if err != nil {
			status = 400
			w.Header().Set("Cache-Control", "no-cache")
			http.Error(w, err.Error(), status)
			return
		}

		// get game
		game, gameID, err := lichess.GetGame(maybeID)
		if err != nil {
			status = 500
			w.Header().Set("Cache-Control", "no-cache")
			http.Error(w, err.Error(), status)
			return
		}

		// write gif
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.gif\"", gameID))
		w.Header().Set("filename", gameID+".gif")
		if env() == "production" {
			w.Header().Set("Cache-Control", cacheControl(1296000))
		} else {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		err = gifmaker.GenerateGIF(game, gameID, getReversed(r), getSpeed(r), getTheme(r), w, maxConcurrency)
		if err != nil {
			status = 500
			w.Header().Set("Cache-Control", "no-cache")
			http.Error(w, err.Error(), status)
			return
		}
		status = 200
	}
}

func getReversed(r *http.Request) bool {
	if s, ok := r.URL.Query()["reversed"]; ok && len(s) == 1 {
		return s[0] == "true"
	}
	return false
}

func getSpeed(r *http.Request) float64 {
	if s, ok := r.URL.Query()["speed"]; ok && len(s) == 1 {
		if speed, err := strconv.ParseFloat(s[0], 64); err == nil && speed > 0 && speed <= 10 {
			return speed
		}
	}
	return 1.0 // default speed
}

func getTheme(r *http.Request) string {
	if s, ok := r.URL.Query()["theme"]; ok && len(s) == 1 {
		validThemes := map[string]bool{
			"brown": true, "blue": true, "green": true, "purple": true,
		}
		if validThemes[s[0]] {
			return s[0]
		}
	}
	return "brown" // default theme
}

func getIDFromQuery(r *http.Request) (string, error) {
	split := strings.Split(r.URL.Path, "/")
	if len(split) < 4 || len(split[3]) < 8 {
		return "", errors.New("No id provided. Please visit /some-id. Example: /bR4b8jno")
	}
	return split[3], nil
}

func cacheControl(seconds int) string {
	return fmt.Sprintf("max-age=%d, public, must-revalidate, proxy-revalidate", seconds)
}

func env() string {
	fromEnv := os.Getenv("APP_ENV")
	if len(fromEnv) > 0 {
		return fromEnv
	}
	return "development"
}

// pgnHandler accepts POST requests containing a PGN (form field `pgn` or raw body)
// validates the PGN by attempting to parse it, and returns a generated GIF.
func pgnHandler(maxConcurrency int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var status int
		logf := func() { log.Println(r.Method, r.URL, status, time.Since(start)) }
		defer logf()

		if r.Method != http.MethodPost {
			status = 405
			http.Error(w, "method not allowed", status)
			return
		}

		pgnText, err := readPGNInput(r)
		if err != nil {
			status = 400
			http.Error(w, err.Error(), status)
			return
		}

		if pgnText == "" {
			status = 400
			http.Error(w, "No PGN provided. Send form field 'pgn' or raw PGN in the request body.", status)
			return
		}

		// Validate PGN by parsing
		parsed, err := chess.PGN(strings.NewReader(pgnText))
		if err != nil {
			status = 400
			http.Error(w, "invalid PGN: "+err.Error(), status)
			return
		}

		game := chess.NewGame(parsed)
		gameID := "pgn-" + strconv.FormatInt(time.Now().Unix(), 10)

		// Options from form/query
		reversed := r.FormValue("reversed") == "true"

		speed := 1.0
		if s := r.FormValue("speed"); s != "" {
			if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 && v <= 10 {
				speed = v
			}
		}

		theme := r.FormValue("theme")
		validThemes := map[string]bool{"brown": true, "blue": true, "green": true, "purple": true}
		if !validThemes[theme] {
			theme = "brown"
		}

		// write gif
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.gif\"", gameID))
		w.Header().Set("filename", gameID+".gif")
		if env() == "production" {
			w.Header().Set("Cache-Control", cacheControl(1296000))
		} else {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}

		if err := gifmaker.GenerateGIF(game, gameID, reversed, speed, theme, w, maxConcurrency); err != nil {
			status = 500
			w.Header().Set("Cache-Control", "no-cache")
			http.Error(w, err.Error(), status)
			return
		}

		status = 200
	}
}

func readPGNInput(r *http.Request) (string, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", fmt.Errorf("could not parse uploaded form: %w", err)
		}
		return strings.TrimSpace(r.FormValue("pgn")), nil
	}

	if err := r.ParseForm(); err != nil {
		return "", fmt.Errorf("could not parse form: %w", err)
	}
	if pgn := strings.TrimSpace(r.FormValue("pgn")); pgn != "" {
		return pgn, nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("could not read body: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}
