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

func TestRenderedSVGDecodesToIRLLinkWithHCorrection(t *testing.T) {
	urls := []string{"https://dad.example/r/aBc123_-?via=irl", "https://donors.dynamic-aid.example/r/abcdefghijklmnopqrstuvwxyz123456?via=irl"}
	for _, url := range urls {
		svg, err := SVG(url)
		if err != nil {
			t.Fatal(err)
		}
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
		if err = xml.Unmarshal(svg, &document); err != nil {
			t.Fatal(err)
		}
		if document.Rect.Fill != "white" || document.Path.Fill != "black" || document.Width != document.Height {
			t.Fatal("scan contrast changed")
		}
		raster := image.NewGray(image.Rect(0, 0, document.Width, document.Height))
		draw.Draw(raster, raster.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		pattern := regexp.MustCompile(`M([0-9]+) ([0-9]+)h1v1h-1z`)
		cells := pattern.FindAllStringSubmatch(document.Path.D, -1)
		if len(cells) == 0 || pattern.ReplaceAllString(document.Path.D, "") != "" {
			t.Fatal("unexpected SVG modules")
		}
		for _, cell := range cells {
			x, _ := strconv.Atoi(cell[1])
			y, _ := strconv.Atoi(cell[2])
			modules := document.Width / 10
			if x < 4 || y < 4 || x >= modules-4 || y >= modules-4 {
				t.Fatal("quiet zone encroached")
			}
			draw.Draw(raster, image.Rect(x*10, y*10, (x+1)*10, (y+1)*10), image.NewUniform(color.Black), image.Point{}, draw.Src)
		}
		bitmap, err := gozxing.NewBinaryBitmapFromImage(raster)
		if err != nil {
			t.Fatal(err)
		}
		result, err := decoder.NewQRCodeReader().Decode(bitmap, nil)
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
