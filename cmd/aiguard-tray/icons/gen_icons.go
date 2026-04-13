//go:build ignore

// Run this once to generate icon PNG bytes embedded into the binary.
// Usage: go run gen_icons.go
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
)

func main() {
	writeIcon("icon_green.png", color.RGBA{34, 197, 94, 255})
	writeIcon("icon_red.png", color.RGBA{239, 68, 68, 255})
	writeIcon("icon_yellow.png", color.RGBA{234, 179, 8, 255})
}

func writeIcon(name string, c color.RGBA) {
	size := 22
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)

	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size)/2 - 2

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			if math.Sqrt(dx*dx+dy*dy) <= r {
				img.Set(x, y, c)
			}
		}
	}

	f, _ := os.Create(name)
	defer f.Close()
	png.Encode(f, img)
}
