package qq

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"time"

	"github.com/ericpauley/go-quantize/quantize"

	"github.com/TundraWork/Ex-Otogi/pkg/webpanim"
)

// QQ refuses a bot's upload of an animated WebP (HTTP 400, 40034002, the
// format unsupported; tried on 2026-11-27), though its users post them, and
// takes a still one. So an animated WebP goes to QQ as a GIF.

const (
	// maxGIFMemory bounds converting an animated WebP to a GIF: the canvas
	// and the largest frame decoded, at 4 bytes a pixel, and every frame
	// kept as a paletted picture, a byte a pixel, until the GIF is encoded.
	// It is the media module's bound on decoding an animation.
	maxGIFMemory = 80 << 20
	// maxGIFPixels is the most pixels, all of an animated WebP's frames
	// together, converted to a GIF. The conversion happens while a reply
	// goes out, with someone waiting for the picture, so it takes 5 seconds
	// at most: on the NAS the test deployment runs on, frames decode,
	// composite, get their palettes, and are dithered and encoded at about
	// 6.5 million pixels a second (BenchmarkGIFOf, frames of many colors),
	// so 5 seconds is about 32 million: a 512x512 animation may have 120
	// frames.
	maxGIFPixels = 30 << 20
	// maxGIFBytes is the largest GIF uploaded: QQ refuses uploads over about
	// 10 MB, and the driver uploads in one request.
	maxGIFBytes = 9 << 20
)

// errGIFTooLarge reports an animated WebP too large to convert, or a GIF
// too large to upload.
var errGIFTooLarge = errors.New("too large")

// gifOf converts an animated WebP to a GIF: every frame, composited, as a
// whole picture that replaces the one before (disposed to the background),
// each with a palette of its own of 255 colors chosen by median cut over
// its opaque pixels, and a transparent one. A pixel at least half opaque is
// drawn opaque, dithered (dither); the rest is transparent: GIF
// has no partial transparency, and alpha is made all or nothing before
// dithering so its error spreads to no neighbor. A frame's duration is
// rounded to GIF's hundredths of a second, at least 2: many players play
// one of 1 or less at 100 ms. It plays as many times as the WebP does.
func gifOf(data []byte) ([]byte, error) {
	a, err := webpanim.Read(data)
	if err != nil {
		return nil, err
	}
	canvas := int64(a.Canvas.Dx()) * int64(a.Canvas.Dy())
	if size := 4*(canvas+a.Largest()) + canvas*int64(a.Frames()); size > maxGIFMemory {
		return nil, fmt.Errorf("%d MiB to convert: %w", size>>20, errGIFTooLarge)
	}
	if pixels := a.Pixels(); pixels > maxGIFPixels {
		return nil, fmt.Errorf("%d million pixels to convert: %w", pixels/1e6, errGIFTooLarge)
	}
	g := &gif.GIF{LoopCount: gifLoops(a.Loops)}
	q := quantize.MedianCutQuantizer{Aggregation: quantize.Mean, AddTransparent: true,
		Weighting: func(m image.Image, x, y int) uint32 {
			if _, _, _, alpha := m.At(x, y).RGBA(); alpha == 0 {
				return 0 // transparent: the palette's transparent color
			}
			return 1
		}}
	solid := image.NewRGBA(a.Canvas)
	err = a.Composite(func(_ int, frame *image.RGBA, d time.Duration) error {
		for i := 0; i < len(frame.Pix); i += 4 {
			alpha := frame.Pix[i+3]
			if alpha < 128 {
				copy(solid.Pix[i:i+4], []byte{0, 0, 0, 0})
				continue
			}
			for c := range 3 { // premultiplied: made opaque, the color as it is
				solid.Pix[i+c] = uint8((uint32(frame.Pix[i+c])*255 + uint32(alpha)/2) / uint32(alpha))
			}
			solid.Pix[i+3] = 255
		}
		p := image.NewPaletted(a.Canvas, q.Quantize(make(color.Palette, 0, 256), solid))
		dither(p, solid)
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, max(2, int((d+5*time.Millisecond)/(10*time.Millisecond))))
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		return nil, fmt.Errorf("encode gif: %w", err)
	}
	if b.Len() > maxGIFBytes {
		return nil, fmt.Errorf("a GIF of %d MiB: %w", b.Len()>>20, errGIFTooLarge)
	}
	return b.Bytes(), nil
}

// dither draws src, each pixel opaque or transparent, on p, whose palette
// has one transparent color: a transparent pixel as it, an opaque one as
// the nearest opaque color, its error spread to the pixels right and below
// (Floyd-Steinberg) but never into transparent ones. The nearest color is
// looked up once for each cell of 5 bits a channel, which draw.FloydSteinberg
// searches the palette for at every pixel: 85% of the conversion's time.
func dither(p *image.Paletted, src *image.RGBA) {
	transparent, opaque := uint8(0), []uint8{}
	for i, c := range p.Palette {
		if _, _, _, a := c.RGBA(); a == 0 {
			transparent = uint8(i)
		} else {
			opaque = append(opaque, uint8(i))
		}
	}
	rgb := make([][3]int32, len(p.Palette))
	for i, c := range p.Palette {
		r, g, b, _ := c.RGBA()
		rgb[i] = [3]int32{int32(r >> 8), int32(g >> 8), int32(b >> 8)}
	}
	var cells [1 << 15]int16
	for i := range cells {
		cells[i] = -1
	}
	nearest := func(r, g, b int32) uint8 {
		cell := r>>3<<10 | g>>3<<5 | b>>3
		if cells[cell] < 0 {
			best, bestD := 0, int32(1<<30)
			for _, i := range opaque {
				dr, dg, db := rgb[i][0]-r, rgb[i][1]-g, rgb[i][2]-b
				if d := dr*dr + dg*dg + db*db; d < bestD {
					best, bestD = int(i), d
				}
			}
			cells[cell] = int16(best)
		}
		return uint8(cells[cell])
	}
	w, h := p.Rect.Dx(), p.Rect.Dy()
	errs, next := make([][3]int32, w+2), make([][3]int32, w+2) // offset by one, for x-1 and x+1
	for y := range h {
		for x := range w {
			o := src.PixOffset(src.Rect.Min.X+x, src.Rect.Min.Y+y)
			at := p.PixOffset(p.Rect.Min.X+x, p.Rect.Min.Y+y)
			if src.Pix[o+3] == 0 || len(opaque) == 0 {
				p.Pix[at] = transparent
				continue
			}
			var c [3]int32
			for k := range 3 {
				c[k] = min(255, max(0, int32(src.Pix[o+k])+errs[x+1][k]/16))
			}
			i := nearest(c[0], c[1], c[2])
			p.Pix[at] = i
			for k := range 3 {
				e := c[k] - rgb[i][k]
				if x+1 < w && src.Pix[o+4+3] != 0 {
					errs[x+2][k] += e * 7
				}
				if y+1 < h {
					below := o + src.Stride
					if x > 0 && src.Pix[below-4+3] != 0 {
						next[x][k] += e * 3
					}
					if src.Pix[below+3] != 0 {
						next[x+1][k] += e * 5
					}
					if x+1 < w && src.Pix[below+4+3] != 0 {
						next[x+2][k] += e
					}
				}
			}
		}
		errs, next = next, errs
		clear(next)
	}
}

// gifLoops is a WebP's loop count, how many times it plays (0 for ever), as
// image/gif's LoopCount gives it: 0 for ever, -1 once, n for n+1 times.
func gifLoops(plays int) int {
	switch plays {
	case 0:
		return 0
	case 1:
		return -1
	}
	return plays - 1
}
