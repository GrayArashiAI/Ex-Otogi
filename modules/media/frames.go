package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/png"
	"time"
)

// maxDecoded is the most memory decoding one GIF may take, in bytes, as
// gifDecoded counts it. A file's size says little: frames of one color
// compress about a thousandfold, so a 185 KB GIF of fifty 2000x2000 frames
// decodes to 192 MiB, and Handle is unordered. Of eku's 61 GIFs, the
// largest decoded to 67 MiB (70 frames of 950x950) and the next to 30 MiB,
// so 80 MiB takes them all, and three at once stay under 250 MB.
const maxDecoded = 80 << 20

// errTooLarge reports an animation too large to decode: a GIF or an
// animated WebP past maxDecoded, or a WebP past maxWebPPixels.
var errTooLarge = errors.New("decodes to more than the limit")

// gifDecoded is how much memory decoding data takes, in bytes, read from
// its blocks without decompressing them: each frame's pixels, one byte
// each, and two RGBA canvases (the one frames are drawn on, and the one
// kept for frames disposed to the previous). It fails on data that is not
// a whole GIF.
//
// It must walk the blocks as image/gif does (readExtension and
// readImageDescriptor in its reader.go): where the two would read a block
// differently, a GIF can look small here and still decode in full. So a
// block image/gif reads in a way it doesn't spell out as sub-blocks is
// accepted only in the shape that reads the same both ways, and anything
// else is refused. FuzzGIFDecoded holds the two to this.
func gifDecoded(data []byte) (int64, error) {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return 0, errors.New("not a gif")
	}
	width, height := int64(binary.LittleEndian.Uint16(data[6:])), int64(binary.LittleEndian.Uint16(data[8:]))
	i := 13
	if data[10]&0x80 != 0 { // a global color table
		i += 3 << ((data[10] & 7) + 1)
	}
	truncated := errors.New("truncated gif")
	// subBlocks skips data sub-blocks up to their terminator, as image/gif's
	// readBlock and blockReader do: neither reads past the first empty one.
	subBlocks := func() error {
		for i < len(data) {
			n := int(data[i])
			i++
			if n == 0 {
				return nil
			}
			i += n
		}
		return truncated
	}
	var frames int64
	for i < len(data) {
		switch data[i] {
		case 0x21: // an extension: introducer, label, then by label
			if i+2 >= len(data) {
				return 0, truncated
			}
			label, size := data[i+1], data[i+2]
			switch {
			case label == 0xF9: // graphic control: image/gif reads six bytes, a size of 4 to a terminator
				if size != 4 || i+7 >= len(data) || data[i+7] != 0 {
					return 0, errors.New("gif: bad graphic control extension")
				}
				i += 8
			case label == 0x01 && size == 12, // plain text: image/gif skips 13 bytes, which is one sub-block only when the first is 12
				label == 0xFE,              // comment: sub-blocks
				label == 0xFF && size != 0: // application: image/gif skips size bytes, then reads sub-blocks; a size of 0 is not a terminator to it
				i += 2
				if err := subBlocks(); err != nil {
					return 0, err
				}
			default:
				return 0, fmt.Errorf("gif: extension 0x%02x of size %d", label, size)
			}
		case 0x2C: // an image: descriptor, color table, code size, sub-blocks
			if i+10 > len(data) {
				return 0, truncated
			}
			w, h := int64(binary.LittleEndian.Uint16(data[i+5:])), int64(binary.LittleEndian.Uint16(data[i+7:]))
			frames += w * h
			if width*height == 0 {
				width, height = w, h // the decoder takes the first frame's size
			}
			if data[i+9]&0x80 != 0 { // a local color table
				i += 3 << ((data[i+9] & 7) + 1)
			}
			i += 11
			if err := subBlocks(); err != nil {
				return 0, err
			}
		case 0x3B: // the trailer
			return frames + 8*width*height, nil
		default:
			return 0, fmt.Errorf("gif: unknown block 0x%02x", data[i])
		}
	}
	return 0, truncated // image/gif fails without a trailer
}

// animationFrames is how many frames of an animation the model sees. On
// eku's chats, 28 stickers with known meanings, as Gemini 3.5 Flash-Lite
// saw them: the GIF itself (only its first frame is read) got 9 of 26
// wrong, the GIF as an mp4 4 of 25, six frames in order 1 of 20.
const animationFrames = 6

// minAnimationFrames is the fewest frames that make a GIF or an animated
// WebP an animation;
// one with fewer is described as a still image.
const minAnimationFrames = 3

// gifFrames decodes a GIF and composites its frames as a browser would,
// honoring each frame's disposal, and returns n of them spread evenly from
// the first to the last as PNG, how many frames it has, and how long it
// plays. A delay of 0 or 1 (hundredths of a second) plays as 100 ms, as in
// Chrome and Firefox. A GIF that would take more than maxDecoded is not
// decoded: the error wraps errTooLarge.
func gifFrames(data []byte, n int) (frames [][]byte, count int, total time.Duration, err error) {
	size, err := gifDecoded(data)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode gif: %w", err)
	}
	if size > maxDecoded {
		return nil, 0, 0, fmt.Errorf("decode gif: %d MiB: %w", size>>20, errTooLarge)
	}
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
	want := spread(count, n)

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

// spread picks n of count frames, spread evenly from the first to the last,
// or all of them when there are no more than n.
func spread(count, n int) map[int]bool {
	n = min(n, count)
	want := map[int]bool{}
	for i := range n {
		at := 0
		if n > 1 {
			at = (i*(count-1) + (n-1)/2) / (n - 1)
		}
		want[at] = true
	}
	return want
}
