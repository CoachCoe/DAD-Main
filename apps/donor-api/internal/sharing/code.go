package sharing

import (
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

// SVG encodes only the server-built IRL referral URL. Modules are unstyled black
// on white with H correction and the encoder's four-module quiet zone retained.
func SVG(url string) ([]byte, error) {
	code, err := qrcode.New(url, qrcode.Highest)
	if err != nil {
		return nil, err
	}
	bitmap := code.Bitmap()
	size := len(bitmap)
	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges"><rect width="100%%" height="100%%" fill="white"/><path fill="black" d="`, size, size, size*10, size*10)
	for y, row := range bitmap {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&out, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	out.WriteString(`"/></svg>`)
	return []byte(out.String()), nil
}
