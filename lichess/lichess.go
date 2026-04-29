package lichess

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aherve/giflichess/gifmaker"
	"github.com/notnil/chess"
)

// GenerateFile generates a file from an url or gameID, into `outFile`. `reversed` can be set to true to view the game from black's perspective
func GenerateFile(urlOrID string, reversed bool, outFile string, maxConcurrency int) error {
	fmt.Printf("generating file %s from game %s...\n", outFile, urlOrID)
	game, gameID, err := GetGame(urlOrID)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(outFile, os.O_WRONLY|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	gifmaker.GenerateGIF(game, gameID, reversed, 1.0, "brown", f, maxConcurrency)
	fmt.Printf("gif successfully outputed to %s\n", outFile)
	return nil
}

// GetGame extracts the PGN from a lichess game url
func GetGame(pathOrID string) (*chess.Game, string, error) {
	id, err := gameID(pathOrID)
	if err != nil {
		return nil, "", err
	}
	// Use a client with timeout and a User-Agent to avoid being blocked by some hosts
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", "https://lichess.org/game/export/"+id, nil)
	if err != nil {
		return nil, id, err
	}
	req.Header.Set("User-Agent", "giflichess/cli")

	resp, err := client.Do(req)
	if err != nil {
		return nil, id, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// read a small prefix of the body to include in the error message
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, id, fmt.Errorf("lichess responded with status %d: %s", resp.StatusCode, strings.ReplaceAll(strings.TrimSpace(string(snippet)), "\n", " "))
	}

	pgn, err := chess.PGN(resp.Body)
	if err != nil {
		return nil, id, err
	}

	return chess.NewGame(pgn), id, nil
}

// gameID extracts the id of a lichess game from either analyze url, game url, or id
func gameID(pathOrID string) (string, error) {

	matchID, err := regexp.MatchString(`^[a-zA-Z0-9]{8,}$`, pathOrID)
	if err != nil {
		return "", err
	}

	if matchID {
		return pathOrID[0:8], nil
	}

	u, err := url.Parse(pathOrID)
	if err != nil {
		return "", err
	}

	split := strings.Split(u.Path, "/")
	if len(split) < 2 || len(split[1]) < 8 {
		return "", fmt.Errorf("could not find id from string \"%s\"", pathOrID)
	}
	return split[1][0:8], nil
}
