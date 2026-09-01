package main

// Generates three 16x16 solid-dot ICO files (green/grey/red) for the tray,
// written as Go byte slices into internal/tray/icons_windows.go. Run once.
import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"os"
)

func ico(c color.RGBA) []byte {
	const n = 16
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	cx, cy, r := 7.5, 7.5, 6.5
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			} else {
				img.Set(x, y, color.RGBA{0, 0, 0, 0})
			}
		}
	}
	// BMP (DIB) payload: 40-byte header, BGRA pixels bottom-up, then AND mask.
	var dib bytes.Buffer
	binary.Write(&dib, binary.LittleEndian, uint32(40))       // header size
	binary.Write(&dib, binary.LittleEndian, int32(n))         // width
	binary.Write(&dib, binary.LittleEndian, int32(n*2))       // height (XOR+AND)
	binary.Write(&dib, binary.LittleEndian, uint16(1))        // planes
	binary.Write(&dib, binary.LittleEndian, uint16(32))       // bpp
	binary.Write(&dib, binary.LittleEndian, uint32(0))        // compression
	binary.Write(&dib, binary.LittleEndian, uint32(0))        // image size
	binary.Write(&dib, binary.LittleEndian, [4]int32{})       // ppm + colors
	for y := n - 1; y >= 0; y-- {
		for x := 0; x < n; x++ {
			px := img.RGBAAt(x, y)
			dib.Write([]byte{px.B, px.G, px.R, px.A})
		}
	}
	maskRow := 4 // 16 bits padded to 4 bytes
	dib.Write(make([]byte, maskRow*n))

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1)) // type icon
	binary.Write(&out, binary.LittleEndian, uint16(1)) // count
	out.Write([]byte{n, n, 0, 0})                      // w,h,colors,reserved
	binary.Write(&out, binary.LittleEndian, uint16(1)) // planes
	binary.Write(&out, binary.LittleEndian, uint16(32))
	binary.Write(&out, binary.LittleEndian, uint32(dib.Len()))
	binary.Write(&out, binary.LittleEndian, uint32(22)) // offset
	out.Write(dib.Bytes())
	return out.Bytes()
}

func main() {
	green := ico(color.RGBA{46, 160, 67, 255})
	grey := ico(color.RGBA{140, 148, 158, 255})
	red := ico(color.RGBA{218, 54, 51, 255})
	var b bytes.Buffer
	fmt.Fprint(&b, "//go:build windows\n\npackage main\n\n// Generated 16x16 solid-dot tray icons. Regenerate with `go run ./tools/genico <path>`.\n\n")
	for _, e := range []struct{ name string; data []byte }{{"iconGreen", green}, {"iconGrey", grey}, {"iconRed", red}} {
		name, data := e.name, e.data
		fmt.Fprintf(&b, "var %s = []byte{", name)
		for i, by := range data {
			if i%16 == 0 {
				b.WriteString("\n\t")
			}
			fmt.Fprintf(&b, "0x%02x, ", by)
		}
		b.WriteString("\n}\n\n")
	}
	os.WriteFile(os.Args[1], b.Bytes(), 0o644)
	fmt.Println("wrote", os.Args[1])
}
