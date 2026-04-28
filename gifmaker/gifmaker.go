package gifmaker

import (
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aherve/chessimg"
	"github.com/aherve/gopool"
	"github.com/notnil/chess"
)

type imgOutput struct {
	index int
	img   *image.Paletted
	err   error
}

// Declare colors for different themes
var themes = map[string][]color.RGBA{
	"brown": {
		{181, 136, 99, 255},  // dark
		{240, 217, 181, 255}, // light
	},
	"blue": {
		{70, 130, 180, 255},  // dark
		{173, 216, 230, 255}, // light
	},
	"green": {
		{119, 149, 86, 255},  // dark
		{235, 236, 208, 255}, // light
	},
	"purple": {
		{128, 70, 153, 255},  // dark
		{218, 185, 234, 255}, // light
	},
}

// Default theme colors (brown)
var darkColor = color.RGBA{181, 136, 99, 255}
var lightColor = color.RGBA{240, 217, 181, 255}

func whiteName(game *chess.Game) string {
	var elo, name string
	for _, tag := range game.TagPairs() {
		if strings.ToLower(tag.Key) == "white" {
			name = tag.Value
		}
		if strings.ToLower(tag.Key) == "whiteelo" {
			elo = tag.Value
		}
		if len(elo) > 0 && len(name) > 0 {
			break
		}
	}
	if len(name) > 0 && len(elo) > 0 {
		return fmt.Sprintf("%s (%s)", name, elo)
	} else if len(name) > 0 {
		return name
	}
	return "unknown"
}

func blackName(game *chess.Game) string {
	var elo, name string
	for _, tag := range game.TagPairs() {
		if strings.ToLower(tag.Key) == "black" {
			name = tag.Value
		}
		if strings.ToLower(tag.Key) == "blackelo" {
			elo = tag.Value
		}
		if len(elo) > 0 && len(name) > 0 {
			break
		}
	}
	if len(name) > 0 && len(elo) > 0 {
		return fmt.Sprintf("%s (%s)", name, elo)
	} else if len(name) > 0 {
		return name
	}
	return "unknown"
}

// GenerateGIF will use *chess.Game to write a gif into an io.Writer
// This uses inkscape & imagemagick as a dependency
func GenerateGIF(game *chess.Game, gameID string, reversed bool, speed float64, theme string, size int, out io.Writer, maxConcurrency int) error {

	// Set theme colors
	if themeColors, exists := themes[theme]; exists {
		darkColor = themeColors[0]
		lightColor = themeColors[1]
	} else {
		// Default to brown theme
		darkColor = themes["brown"][0]
		lightColor = themes["brown"][1]
	}

	// Generate PNGs
	pool := gopool.NewPool(maxConcurrency)
	for i, pos := range game.Positions() {
		pool.Add(1)
		go drawPNG(pos, whiteName(game), blackName(game), reversed, size, fileBaseFor(gameID, i), pool)
		defer cleanup(gameID, i)
	}
	pool.Wait()

	// add Result to last png image
	fileName := fileBaseFor(gameID, len(game.Positions())-1) + ".png"
	cmd := exec.Command("gifmaker/addResult.sh", fileName, game.Outcome().String())
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	// Generate atomic GIFs
	images := make([]*image.Paletted, len(game.Positions()), len(game.Positions()))
	imgChan := make(chan imgOutput)
	quit := make(chan bool)

	go func() {
		for i := range game.Positions() {
			pool.Add(1)
			go func(gameID string, i int, outChan chan imgOutput, pool *gopool.GoPool) {
				defer pool.Done()
				encoded, err := encodeGIFImage(gameID, i)
				outChan <- imgOutput{i, encoded, err}
				return
			}(gameID, i, imgChan, pool)

		}
		pool.Wait()
		quit <- true
	}()

loop:
	for {
		select {
		case res := <-imgChan:
			if res.err != nil {
				return res.err
			}
			images[res.index] = res.img
		case <-quit:
			break loop
		}
	}

	// Generate final GIF
	outGIF := &gif.GIF{}
	for i, img := range images {

		// Add new frame to animated GIF
		outGIF.Image = append(outGIF.Image, img)

		// Calculate delays based on speed (delays are in hundredths of a second)
		baseDelay := 0
		if i == len(game.Positions())-1 {
			baseDelay = 450 // final position stays longer
		} else if i < 10 {
			baseDelay = 50 // opening moves are faster
		} else {
			baseDelay = 120 // normal moves
		}

		// Adjust delay based on speed parameter
		adjustedDelay := int(float64(baseDelay) / speed)
		if adjustedDelay < 5 { // minimum delay to prevent too fast animation
			adjustedDelay = 5
		}
		outGIF.Delay = append(outGIF.Delay, adjustedDelay)
	}

	gif.EncodeAll(out, outGIF)
	return nil
}

// encodeGIFImage reads a png from gameID & index, and returns a palettedImage
func encodeGIFImage(gameID string, i int) (*image.Paletted, error) {
	f, err := os.Open(fileBaseFor(gameID, i) + ".png")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	inPNG, err := png.Decode(f)
	if err != nil {
		return nil, err
	}

	bounds := inPNG.Bounds()
	myPalette := append(palette.WebSafe, []color.Color{lightColor, darkColor}...)
	palettedImage := image.NewPaletted(bounds, myPalette)
	draw.Draw(palettedImage, palettedImage.Rect, inPNG, bounds.Min, draw.Over)

	return palettedImage, nil
}

func drawPNG(pos *chess.Position, whiteName string, blackName string, reversed bool, size int, filebase string, pool *gopool.GoPool) error {
	defer pool.Done()

	// create file
	f, err := os.Create(filebase + ".svg")
	if err != nil {
		return err
	}

	colors := chessimg.SquareColors(lightColor, darkColor)

	// write board SVG to file
	if reversed {
		err = chessimg.ReversedSVG(f, pos.Board(), colors)
	} else {
		err = chessimg.SVG(f, pos.Board(), colors)
	}
	if err != nil {
		return err
	}

	// close svg file
	f.Close()

	// Use inkscape to convert svg -> png
	cmd := exec.Command(
		"dbus-run-session",
		"inkscape",
		"--export-width="+fmt.Sprintf("%d", size),
		"--export-height="+fmt.Sprintf("%d", size),
		"--export-filename="+filebase+".png",
		filebase+".svg",
	)
	// Suppress GTK warnings in headless environment
	cmd.Env = append(os.Environ(), "GTK_BACKEND=cairo")
	if r := cmd.Run(); r != nil {
		return err
	}

	// add annotation
	cmd = exec.Command("gifmaker/annotate.sh", filebase+".png", whiteName, blackName)
	cmd.Stderr = os.Stderr
	if r := cmd.Run(); r != nil {
		return err
	}
	return nil
}

func cleanup(gameID string, i int) {
	fileBase := fileBaseFor(gameID, i)
	os.Remove(fileBase + ".svg")
	os.Remove(fileBase + ".png")
}

func fileBaseFor(gameID string, i int) string {
	return "/tmp/" + gameID + fmt.Sprintf("%03d", i)
}
