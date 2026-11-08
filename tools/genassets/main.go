// Command genassets draws the Ringmaster icon (logo concept A, the dark tile
// variant) and writes the raster assets the repo ships:
//
//	assets/icon/ringmaster.ico      16-256 px, embedded into the .exe files
//	assets/icon/ringmaster-256.png  for package managers and docs
//	internal/api/ui/favicon.png     32 px browser fallback (favicon.svg is hand-written)
//
// Run from the repo root: go run ./tools/genassets
//
// The shapes are simple enough (a rounded tile, a ring, three dots) to
// rasterize directly with supersampling, so there is no SVG renderer
// dependency and every size comes from the same geometry.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// Geometry in a 64x64 design space, matching assets/logo/*.svg.
const (
	tileRadius = 14.0
	markScale  = 0.8  // the mark sits inside the tile at 80%
	ringR      = 20.0 // ring centre-line radius before scaling
	ringW      = 6.0  // ring stroke width before scaling
	dotR       = 7.0  // dot radius before scaling
)

var (
	ink    = color.NRGBA{0x16, 0x18, 0x1D, 0xFF}
	paper  = color.NRGBA{0xF7, 0xF5, 0xF0, 0xFF}
	accent = color.NRGBA{0xE8, 0xA3, 0x3D, 0xFF}
)

// dots are the three projects on the ring: top (accent), lower left, lower right.
var dots = []struct {
	angle float64 // degrees, 0 = east, clockwise in screen space
	col   color.NRGBA
}{
	{-90, accent},
	{150, paper},
	{30, paper},
}

// sample returns the colour at design-space point (x, y) and whether
// anything is painted there.
func sample(x, y float64) (color.NRGBA, bool) {
	if !inRoundRect(x, y, 0, 0, 64, 64, tileRadius) {
		return color.NRGBA{}, false
	}
	c := ink
	dx, dy := x-32, y-32
	d := math.Hypot(dx, dy)
	if math.Abs(d-ringR*markScale) <= ringW*markScale/2 {
		c = paper
	}
	for _, dot := range dots {
		a := dot.angle * math.Pi / 180
		cx := 32 + ringR*markScale*math.Cos(a)
		cy := 32 + ringR*markScale*math.Sin(a)
		if math.Hypot(x-cx, y-cy) <= dotR*markScale {
			c = dot.col
		}
	}
	return c, true
}

func inRoundRect(x, y, x0, y0, w, h, r float64) bool {
	if x < x0 || y < y0 || x > x0+w || y > y0+h {
		return false
	}
	// Distance from the nearest corner centre, only in the corner regions.
	cx := math.Max(x0+r-x, math.Max(0, x-(x0+w-r)))
	cy := math.Max(y0+r-y, math.Max(0, y-(y0+h-r)))
	return cx*cx+cy*cy <= r*r
}

// render rasterizes the icon at size x size with 8x8 supersampling.
func render(size int) *image.NRGBA {
	const ss = 8
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := 64.0 / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64 // premultiplied sums
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) * scale
					y := (float64(py) + (float64(sy)+0.5)/ss) * scale
					if c, ok := sample(x, y); ok {
						r += float64(c.R)
						g += float64(c.G)
						b += float64(c.B)
						a++
					}
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(r / a)),
				G: uint8(math.Round(g / a)),
				B: uint8(math.Round(b / a)),
				A: uint8(math.Round(a / (ss * ss) * 255)),
			})
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// writeICO writes a Vista-style .ico whose entries are PNG-encoded.
func writeICO(path string, sizes []int) error {
	var pngs [][]byte
	for _, s := range sizes {
		pngs = append(pngs, encodePNG(render(s)))
	}
	var buf bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&buf, le, [3]uint16{0, 1, uint16(len(sizes))}) // reserved, type=icon, count
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s >= 256 {
			dim = 0 // 0 means 256 in the ICO directory
		}
		buf.Write([]byte{dim, dim, 0, 0})            // width, height, palette, reserved
		_ = binary.Write(&buf, le, [2]uint16{1, 32}) // planes, bits per pixel
		_ = binary.Write(&buf, le, [2]uint32{uint32(len(pngs[i])), uint32(offset)})
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return writeFile(path, buf.Bytes())
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func main() {
	steps := []struct {
		path string
		fn   func(string) error
	}{
		{"assets/icon/ringmaster.ico", func(p string) error { return writeICO(p, []int{16, 20, 24, 32, 40, 48, 64, 128, 256}) }},
		{"assets/icon/ringmaster-256.png", func(p string) error { return writeFile(p, encodePNG(render(256))) }},
		{"internal/api/ui/favicon.png", func(p string) error { return writeFile(p, encodePNG(render(32))) }},
	}
	for _, s := range steps {
		if err := s.fn(s.path); err != nil {
			fmt.Fprintln(os.Stderr, "genassets:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", s.path)
	}
}
