package media

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/webpanim"
)

// maxWebPPixels is the most pixels, all of an animated WebP's frames
// together, webpFrames decodes. A message waits 15 seconds at most for its
// media's descriptions before it is judged (chat's describe_wait.judge),
// and a description takes about 5.5 seconds at the median on eku's chats,
// so decoding gets 2 of the 15 and leaves the model its time. On the NAS
// the test deployment runs on, lossy 512x512 frames decode and composite,
// six of them encoded as PNG, at about 37 million pixels a second
// (BenchmarkWebPFrames), so 2 seconds is about 74 million: a 512x512
// animation may have 280 frames.
const maxWebPPixels = 70 << 20

// webpFrames decodes an animated WebP and composites its frames (see
// webpanim), and returns n of them spread evenly from the first to the last
// as PNG, how many frames it has, and how long it plays. An animation whose
// canvas and largest frame would take more than maxDecoded, or whose frames
// have more than maxWebPPixels in all, is not decoded: the error wraps
// errTooLarge.
func webpFrames(data []byte, n int) (frames [][]byte, count int, total time.Duration, err error) {
	a, err := webpanim.Read(data)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode %w", err)
	}
	if size := 4 * (int64(a.Canvas.Dx())*int64(a.Canvas.Dy()) + a.Largest()); size > maxDecoded {
		return nil, 0, 0, fmt.Errorf("decode webp: %d MiB: %w", size>>20, errTooLarge)
	}
	if pixels := a.Pixels(); pixels > maxWebPPixels {
		return nil, 0, 0, fmt.Errorf("decode webp: %d million pixels: %w", pixels/1e6, errTooLarge)
	}
	want := spread(a.Frames(), n)
	err = a.Composite(func(i int, canvas *image.RGBA, _ time.Duration) error {
		if !want[i] {
			return nil
		}
		var b bytes.Buffer
		if err := png.Encode(&b, canvas); err != nil {
			return fmt.Errorf("encode frame %d: %w", i, err)
		}
		frames = append(frames, b.Bytes())
		return nil
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode %w", err)
	}
	return frames, a.Frames(), a.Duration(), nil
}
