package sharing

import (
	"encoding/xml"
	"image"
	"image/color"
	"image/draw"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	decoder "github.com/makiuchi-d/gozxing/qrcode"
)

var testURLs = []string{
	"https://dad.example/r/aBc123_-?via=irl",
	"https://donors.dynamic-aid.example/r/abcdefghijklmnopqrstuvwxyz123456?via=irl",
}

var modulePattern = regexp.MustCompile(`M([0-9]+) ([0-9]+)h1v1h-1z`)

// renderedModules returns the SVG's side length in modules and its dark modules.
func renderedModules(t *testing.T, svg []byte) (int, map[image.Point]bool) {
	t.Helper()
	var document struct {
		Width  int `xml:"width,attr"`
		Height int `xml:"height,attr"`
		Rect   struct {
			Fill string `xml:"fill,attr"`
		} `xml:"rect"`
		Path struct {
			D    string `xml:"d,attr"`
			Fill string `xml:"fill,attr"`
		} `xml:"path"`
	}
	if err := xml.Unmarshal(svg, &document); err != nil {
		t.Fatal(err)
	}
	if document.Rect.Fill != "white" || document.Path.Fill != "black" || document.Width != document.Height {
		t.Fatal("scan contrast changed")
	}
	cells := modulePattern.FindAllStringSubmatch(document.Path.D, -1)
	if len(cells) == 0 || modulePattern.ReplaceAllString(document.Path.D, "") != "" {
		t.Fatal("unexpected SVG modules")
	}
	size := document.Width / 10
	dark := map[image.Point]bool{}
	for _, cell := range cells {
		x, _ := strconv.Atoi(cell[1])
		y, _ := strconv.Atoi(cell[2])
		if x < quietZone || y < quietZone || x >= size-quietZone || y >= size-quietZone {
			t.Fatal("quiet zone encroached")
		}
		dark[image.Pt(x, y)] = true
	}
	return size, dark
}

func decode(size int, dark map[image.Point]bool) (*gozxing.Result, error) {
	raster := image.NewGray(image.Rect(0, 0, size*10, size*10))
	draw.Draw(raster, raster.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	for p := range dark {
		draw.Draw(raster, image.Rect(p.X*10, p.Y*10, (p.X+1)*10, (p.Y+1)*10), image.NewUniform(color.Black), image.Point{}, draw.Src)
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(raster)
	if err != nil {
		return nil, err
	}
	return decoder.NewQRCodeReader().Decode(bitmap, nil)
}

func TestRenderedSVGDecodesToIRLLinkWithHCorrection(t *testing.T) {
	for _, url := range testURLs {
		svg, err := SVG(url)
		if err != nil {
			t.Fatal(err)
		}
		result, err := decode(renderedModules(t, svg))
		if err != nil {
			t.Fatal("generated SVG does not scan", err)
		}
		if result.GetText() != url || result.GetResultMetadata()[gozxing.ResultMetadataType_ERROR_CORRECTION_LEVEL] != "H" {
			t.Fatal("QR payload or correction changed")
		}
		if strings.Contains(string(svg), "https:") || strings.Contains(string(svg), "<script") {
			t.Fatal("URL copied into active SVG content")
		}
	}
}

func TestRenderedSVGSurvivesCrackedScreen(t *testing.T) {
	for _, url := range testURLs {
		svg, err := SVG(url)
		if err != nil {
			t.Fatal(err)
		}
		size, dark := renderedModules(t, svg)
		n := size - 2*quietZone
		// Stay right of the left-hand finders and below the top-right finder and
		// its format information row so only data and correction modules are lost.
		width := n / 3
		height := (15*n*n + 100*width - 1) / (100 * width)
		top := max(9, (n-height)/2)
		if top+height > n {
			t.Fatalf("damage block does not fit a %d-module code", n)
		}
		damage := image.Rect(n-width, top, n, top+height).Add(image.Pt(quietZone, quietZone))
		blanked := 0
		for p := range dark {
			if p.In(damage) {
				delete(dark, p)
				blanked++
			}
		}
		if area := damage.Dx() * damage.Dy(); area*100 < 15*n*n || blanked == 0 {
			t.Fatalf("damaged %d modules (%d dark) of %d", area, blanked, n*n)
		}
		result, err := decode(size, dark)
		if err != nil {
			t.Fatal("damaged code no longer scans", err)
		}
		if result.GetText() != url {
			t.Fatal("damaged code decoded to the wrong payload")
		}
	}
}
