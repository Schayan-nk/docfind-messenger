package find

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // decode only
	"image/jpeg"
	_ "image/png" // decode only
	"sync"
)

// Thumbnails for the organisational chart.
//
// The HR system stores whatever was uploaded: the photos measured between 4 KB
// and 402 KB, and forty-five of them is around eighteen megabytes. On the
// phone this is meant to be read on, that is the difference between a chart
// and a stalled page -- so the server sends a small square instead of the
// original.
//
// Written against the standard library rather than adding an imaging
// dependency. A box filter is not the finest resampler there is, but at 96
// pixels inside a circle nobody can tell, and it avoids putting another module
// in the build for one decorative feature.

// thumbMax is the largest thumbnail this will produce. The chart draws them at
// 44-56 CSS pixels, so 128 covers a 2x display with room to spare.
const thumbMax = 128

// thumbCache keeps the results, because the expensive half is decoding a
// 400 KB JPEG and that answer never changes for a given photo.
//
// Bounded by the number of people in the company, which is the natural limit
// here: there is one entry per face, and the chart is the only caller.
var thumbCache sync.Map // key string -> []byte

// thumbnail returns a square JPEG of at most size pixels a side.
//
// Returns the original bytes unchanged if it cannot be decoded -- the column
// holds arbitrary uploads, and a photo this cannot resize is better sent whole
// than not sent at all.
func thumbnail(key string, raw []byte, size int) ([]byte, string) {
	if size <= 0 || size > thumbMax {
		size = thumbMax
	}
	ck := fmt.Sprintf("%s|%d", key, size)
	if v, ok := thumbCache.Load(ck); ok {
		return v.([]byte), "image/jpeg"
	}

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw, imageType(raw)
	}

	// Crop to a centred square first, so a portrait is not squashed into the
	// circle the chart draws it in.
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	sq := image.Rect(0, 0, side, side).Add(image.Pt(
		b.Min.X+(b.Dx()-side)/2,
		b.Min.Y+(b.Dy()-side)/2,
	))

	out := image.NewRGBA(image.Rect(0, 0, size, size))
	boxScale(out, src, sq)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 82}); err != nil {
		return raw, imageType(raw)
	}
	small := buf.Bytes()
	// Never send more than we were given: a tiny source scaled up would be
	// larger than the original for no gain.
	if len(small) >= len(raw) {
		thumbCache.Store(ck, raw)
		return raw, imageType(raw)
	}
	thumbCache.Store(ck, small)
	return small, "image/jpeg"
}

// boxScale averages each destination pixel over the source pixels it covers.
//
// Averaging rather than sampling one pixel per output pixel: at these
// reduction ratios -- often 20:1 -- nearest-neighbour drops almost every
// pixel and turns a face into aliased noise.
func boxScale(dst *image.RGBA, src image.Image, from image.Rectangle) {
	dw, dh := dst.Bounds().Dx(), dst.Bounds().Dy()
	sw, sh := from.Dx(), from.Dy()
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	// A fast path for the case the source is already small.
	if sw <= dw || sh <= dh {
		draw.Draw(dst, dst.Bounds(), src, from.Min, draw.Src)
		return
	}

	for y := 0; y < dh; y++ {
		y0 := from.Min.Y + y*sh/dh
		y1 := from.Min.Y + (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := from.Min.X + x*sw/dw
			x1 := from.Min.X + (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					// RGBA() returns 16-bit values; keep them there and
					// shift once at the end to avoid rounding twice.
					r += uint64(cr)
					g += uint64(cg)
					b += uint64(cb)
					n++
				}
			}
			if n == 0 {
				continue
			}
			// RGBA() gave 16-bit values, averaged above; shift to 8 bits once
			// here rather than rounding at every pixel.
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / n >> 8),
				G: uint8(g / n >> 8),
				B: uint8(b / n >> 8),
				A: 0xFF,
			})
		}
	}
}
