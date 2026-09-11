// genicons 从 NovaVeil 星星面纱图形生成全套位图图标。
// 仅用标准库: 直接以 1024 超采样绘制后盒式降采样抗锯齿, ICO 内嵌 PNG 数据。
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const size = 1024

var brand = color.RGBA{R: 0x46, G: 0x65, B: 0x51, A: 255}

func main() {
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := float64(size) / 100.0

	star := [][2]float64{
		{50, 12}, {56.5, 41.5}, {85, 48}, {56.5, 54.5},
		{50, 84}, {43.5, 54.5}, {15, 48}, {43.5, 41.5},
	}
	fillPolygon(canvas, star, scale)

	strokeVeil(canvas, scale, 6, [][2]float64{
		{16, 68}, {33, 82}, {50, 82}, {67, 82}, {84, 68}})
	strokeVeil(canvas, scale*0.66, 4, [][2]float64{
		{26, 80}, {38, 90}, {50, 90}, {62, 90}, {74, 80}})

	full := downscale(canvas, size/4)

	writePNG("web-app-manifest-512x512.png", resize(full, 512))
	writePNG("web-app-manifest-192x192.png", resize(full, 192))
	writePNG("apple-icon.png", resize(full, 180))
	writeICO("favicon.ico", full, []int{16, 32, 48})
}

func fillPolygon(img *image.NRGBA, poly [][2]float64, scale float64) {
	minY, maxY := math.MaxFloat64, -math.MaxFloat64
	pts := make([][2]float64, len(poly))
	for i, p := range poly {
		pts[i] = [2]float64{p[0] * scale, p[1] * scale}
		if pts[i][1] < minY {
			minY = pts[i][1]
		}
		if pts[i][1] > maxY {
			maxY = pts[i][1]
		}
	}
	for y := int(minY); y <= int(maxY); y++ {
		var xs []float64
		cy := float64(y) + 0.5
		for i := range pts {
			a := pts[i]
			b := pts[(i+1)%len(pts)]
			if (a[1] <= cy && b[1] > cy) || (b[1] <= cy && a[1] > cy) {
				t := (cy - a[1]) / (b[1] - a[1])
				xs = append(xs, a[0]+t*(b[0]-a[0]))
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			for x := int(xs[i]); x <= int(xs[i+1]); x++ {
				img.Set(x, y, brand)
			}
		}
	}
}

func strokeVeil(img *image.NRGBA, scale float64, width float64, ctrl [][2]float64) {
	radius := width * scale / 2
	bez := func(t float64) [2]float64 {
		p := append([][2]float64(nil), ctrl...)
		for k := 0; k < len(ctrl)-1; k++ {
			for i := 0; i < len(p)-1-k; i++ {
				p[i][0] += t * (p[i+1][0] - p[i][0])
				p[i][1] += t * (p[i+1][1] - p[i][1])
			}
		}
		return p[0]
	}
	const steps = 240
	prev := bez(0)
	for i := 1; i <= steps; i++ {
		cur := bez(float64(i) / steps)
		dist := math.Hypot(cur[0]-prev[0], cur[1]-prev[1])
		n := int(dist*2) + 1
		for k := 0; k <= n; k++ {
			f := float64(k) / float64(n)
			fillCircle(img, prev[0]+f*(cur[0]-prev[0]), prev[1]+f*(cur[1]-prev[1]), radius)
		}
		prev = cur
	}
}

func fillCircle(img *image.NRGBA, cx, cy, r float64) {
	for y := int(cy - r); y <= int(cy+r); y++ {
		for x := int(cx - r); x <= int(cx+r); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, brand)
			}
		}
	}
}

func downscale(src *image.NRGBA, target int) *image.NRGBA {
	f := src.Rect.Dx() / target
	dst := image.NewNRGBA(image.Rect(0, 0, target, target))
	for y := 0; y < target; y++ {
		for x := 0; x < target; x++ {
			var r, g, b, a uint32
			for dy := 0; dy < f; dy++ {
				for dx := 0; dx < f; dx++ {
					c := src.NRGBAAt(x*f+dx, y*f+dy)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
					a += uint32(c.A)
				}
			}
			n := uint32(f * f)
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: uint8(a / n),
			})
		}
	}
	return dst
}

func resize(src *image.NRGBA, target int) *image.NRGBA {
	srcSize := src.Rect.Dx()
	ratio := float64(srcSize) / float64(target)
	dst := image.NewNRGBA(image.Rect(0, 0, target, target))
	for y := 0; y < target; y++ {
		sy := int(float64(y) * ratio)
		for x := 0; x < target; x++ {
			sx := int(float64(x) * ratio)
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func writePNG(name string, img image.Image) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	if err := os.WriteFile(name, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
}

func writeICO(name string, full *image.NRGBA, sizes []int) {
	var header bytes.Buffer
	header.Write([]byte{0, 0, 1, 0, byte(len(sizes)), 0})
	dirOffset := int32(6 + 16*len(sizes))
	var blobs [][]byte
	for _, sz := range sizes {
		resized := resize(full, sz)
		var buf bytes.Buffer
		if err := png.Encode(&buf, resized); err != nil {
			panic(err)
		}
		blobs = append(blobs, buf.Bytes())
		w := byte(0)
		h := byte(0)
		if sz < 256 {
			w, h = byte(sz), byte(sz)
		}
		header.Write([]byte{w, h, 0, 0, 1, 0, 32, 0})
		n := uint32(buf.Len())
		header.Write([]byte{byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24)})
		header.Write([]byte{byte(dirOffset), byte(dirOffset >> 8), byte(dirOffset >> 16), byte(dirOffset >> 24)})
		dirOffset += int32(buf.Len())
	}
	final := append(header.Bytes(), bytes.Join(blobs, nil)...)
	if err := os.WriteFile(name, final, 0o644); err != nil {
		panic(err)
	}
}
