package media

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/png"
	"time"
)

// animationFrames is how many frames of an animation the model sees. On
// eku's chats, 28 stickers with known meanings, as Gemini 3.5 Flash-Lite
// saw them: the GIF itself (only its first frame is read) got 9 of 26
// wrong, the GIF as an mp4 4 of 25, six frames in order 1 of 20.
const animationFrames = 6

// minAnimationFrames is the fewest frames that make a GIF an animation;
// one with fewer is described as a still image.
const minAnimationFrames = 3

// gifFrames decodes a GIF and composites its frames as a browser would,
// honoring each frame's disposal, and returns n of them spread evenly from
// the first to the last as PNG, how many frames it has, and how long it
// plays. A delay of 0 or 1 (hundredths of a second) plays as 100 ms, as in
// Chrome and Firefox.
func gifFrames(data []byte, n int) (frames [][]byte, count int, total time.Duration, err error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode gif: %w", err)
	}
	count = len(g.Image)
	if count == 0 {
		return nil, 0, 0, fmt.Errorf("decode gif: no frames")
	}
	for _, d := range g.Delay {
		if d <= 1 {
			d = 10
		}
		total += time.Duration(d) * 10 * time.Millisecond
	}
	if n > count {
		n = count
	}
	want := map[int]bool{}
	for i := range n {
		at := 0
		if n > 1 {
			at = (i*(count-1) + (n-1)/2) / (n - 1)
		}
		want[at] = true
	}

	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	if bounds.Empty() {
		bounds = g.Image[0].Bounds()
	}
	canvas := image.NewRGBA(bounds)
	var saved *image.RGBA
	for i, frame := range g.Image {
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			saved = image.NewRGBA(bounds)
			copy(saved.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		if want[i] {
			var b bytes.Buffer
			if err := png.Encode(&b, canvas); err != nil {
				return nil, 0, 0, fmt.Errorf("encode frame %d: %w", i, err)
			}
			frames = append(frames, b.Bytes())
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, saved.Pix)
		}
	}
	return frames, count, total, nil
}
