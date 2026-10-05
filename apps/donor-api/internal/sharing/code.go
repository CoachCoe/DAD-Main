package sharing

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/boombuler/barcode/qr"
)

const quietZone = 4

// SVG encodes only the server-built IRL referral URL. Modules are unstyled black
// on white with H correction and a four-module quiet zone.
func SVG(url string) ([]byte, error) {
	code, err := qr.Encode(url, qr.H, qr.Auto)
	if err != nil {
		return nil, err
	}
	modules := code.Bounds().Dx()
	size := modules + 2*quietZone
	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges"><rect width="100%%" height="100%%" fill="white"/><path fill="black" d="`, size, size, size*10, size*10)
	for y := range modules {
		for x := range modules {
			if code.At(x, y) == color.Black {
				fmt.Fprintf(&out, "M%d %dh1v1h-1z", x+quietZone, y+quietZone)
			}
		}
	}
	out.WriteString(`"/></svg>`)
	return []byte(out.String()), nil
}
