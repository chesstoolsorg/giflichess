package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aherve/giflichess/gifmaker"
	"github.com/aherve/giflichess/lichess"
)

// Serve starts a server
func Serve(port, maxConcurrency int) {
	// Serve static files
	http.Handle("/", http.FileServer(http.Dir("./static/")))

	// API endpoints
	http.HandleFunc("/api/ping", pingHandler)
	http.HandleFunc("/api/lichess/", lichessGifHandler(maxConcurrency))

	log.Printf("starting %s server on port %v with concurrency=%v\n", env(), port, maxConcurrency)
	log.Printf("Web UI available at: http://localhost:%v\n", port)
	http.ListenAndServe(":"+strconv.Itoa(port), nil)
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
