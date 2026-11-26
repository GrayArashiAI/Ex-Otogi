package webpanim

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
	"time"

	refwebp "github.com/gen2brain/webp"
)

// webpTestFrame is a frame of a test animation: a picture put at at on the
// canvas (at's coordinates even), for ms milliseconds.
type webpTestFrame struct {
	img     image.Image
	at      image.Point
	ms      int
	blend   bool
	dispose bool
}

// riffChunk is a RIFF chunk: id, its size, c, padded to an even size.
func riffChunk(id string, c []byte) []byte {
	b := append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(c)))...)
	b = append(b, c...)
	if len(c)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

// riffFile is a WebP file of chunks.
func riffFile(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c...)
	}
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

func le24(n int) []byte { return []byte{byte(n), byte(n >> 8), byte(n >> 16)} }

// vp8xChunk is an extended header for a w x h canvas with flags.
func vp8xChunk(flags byte, w, h int) []byte {
	return riffChunk("VP8X", append(append([]byte{flags, 0, 0, 0}, le24(w-1)...), le24(h-1)...))
}

// losslessBits is img's VP8L chunk, as libwebp encodes it losslessly.
func losslessBits(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := refwebp.Encode(&b, img, refwebp.Options{Lossless: true, Exact: true}); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	if string(data[12:16]) != "VP8L" {
		t.Fatalf("libwebp wrote %q, want a simple lossless file", data[12:16])
	}
	return data[12:]
}

// webpOf builds an animated WebP on a w x h canvas of frames, each a
// lossless picture, with extra chunks after them.
func webpOf(t testing.TB, w, h int, frames []webpTestFrame, extra ...[]byte) []byte {
	t.Helper()
	chunks := [][]byte{vp8xChunk(0x12, w, h), riffChunk("ANIM", make([]byte, 6))}
	for _, f := range frames {
		size := f.img.Bounds().Size()
		flags := byte(0)
		if !f.blend {
			flags |= 0x02
		}
		if f.dispose {
			flags |= 0x01
		}
		head := append(append(append(append(le24(f.at.X/2), le24(f.at.Y/2)...), le24(size.X-1)...), le24(size.Y-1)...), le24(f.ms)...)
		chunks = append(chunks, riffChunk("ANMF", append(append(head, flags), losslessBits(t, f.img)...)))
	}
	return riffFile(append(chunks, extra...)...)
}

// solid is a w x h picture of c.
func solid(w, h int, c color.Color) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// composited reads data and composites all its frames, each returned as
// PNG, with the frame count and the length.
func composited(data []byte) ([][]byte, int, time.Duration, error) {
	a, err := Read(data)
	if err != nil {
		return nil, 0, 0, err
	}
	var frames [][]byte
	err = a.Composite(func(_ int, canvas *image.RGBA, _ time.Duration) error {
		var b bytes.Buffer
		if err := png.Encode(&b, canvas); err != nil {
			return err
		}
		frames = append(frames, b.Bytes())
		return nil
	})
	return frames, a.Frames(), a.Duration(), err
}

// decodedFrames decodes composited's PNG frames.
func decodedFrames(t *testing.T, frames [][]byte) []image.Image {
	t.Helper()
	var out []image.Image
	for _, f := range frames {
		img, err := png.Decode(bytes.NewReader(f))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, img)
	}
	return out
}

// sameAsLibwebp compares the composited frames of data with
// libwebp's own animation decoder's, which gives them premultiplied, within
// tolerance on each channel.
func sameAsLibwebp(t *testing.T, name string, data []byte, tolerance int) {
	t.Helper()
	ref, err := refwebp.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: libwebp: %v", name, err)
	}
	frames, count, total, err := composited(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var want time.Duration
	for _, d := range ref.Delay {
		want += time.Duration(d) * time.Millisecond
	}
	if count != len(ref.Image) || len(frames) != count || total != want {
		t.Fatalf("%s: %d frames of %d, %v, libwebp %d, %v", name, len(frames), count, total, len(ref.Image), want)
	}
	for i, got := range decodedFrames(t, frames) {
		r := ref.Image[i]
		if got.Bounds() != r.Bounds() {
			t.Fatalf("%s: frame %d is %v, libwebp's %v", name, i, got.Bounds(), r.Bounds())
		}
		for y := r.Bounds().Min.Y; y < r.Bounds().Max.Y; y++ {
			for x := r.Bounds().Min.X; x < r.Bounds().Max.X; x++ {
				g := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
				w := color.RGBAModel.Convert(r.At(x, y)).(color.RGBA)
				for c, d := range []int{int(g.R) - int(w.R), int(g.G) - int(w.G), int(g.B) - int(w.B), int(g.A) - int(w.A)} {
					if d > tolerance || -d > tolerance {
						t.Fatalf("%s: frame %d at (%d,%d) channel %d: %v, libwebp %v", name, i, x, y, c, g, w)
					}
				}
			}
		}
	}
}

// Frames are composited as libwebp's own decoder does: placed at their
// offsets, blended or not, disposed or not, the canvas starting
// transparent. Opaque and transparent pixels match it exactly; half
// transparent ones are blended in Go's premultiplied colors, libwebp's in
// its own rounding, within a step of each other.
func TestCompositeAsLibwebp(t *testing.T) {
	red, blue := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 255}
	halfGreen := color.NRGBA{0, 255, 0, 128}
	sameAsLibwebp(t, "offsets, blending and disposal", webpOf(t, 8, 8, []webpTestFrame{
		{img: solid(8, 8, red), ms: 30},
		{img: solid(4, 4, blue), at: image.Pt(2, 2), ms: 40, blend: true, dispose: true},
		{img: solid(4, 2, color.NRGBA{}), at: image.Pt(4, 0), ms: 50},              // replaces: a transparent hole
		{img: solid(2, 6, blue), at: image.Pt(0, 2), ms: 60, blend: true},          // over the disposed square's place
		{img: solid(6, 6, color.NRGBA{}), at: image.Pt(2, 2), ms: 70, blend: true}, // blending transparency changes nothing
	}), 0)
	sameAsLibwebp(t, "half transparent", webpOf(t, 6, 6, []webpTestFrame{
		{img: solid(6, 6, red), ms: 30},
		{img: solid(4, 4, halfGreen), at: image.Pt(2, 2), ms: 30, blend: true},
		{img: solid(2, 2, halfGreen), at: image.Pt(0, 0), ms: 30},
		{img: solid(4, 4, halfGreen), at: image.Pt(2, 0), ms: 30, blend: true},
	}), 1)

	// Animations as libwebp's encoder makes them, picking its own
	// rectangles, blending, and disposal: a square moving over a
	// transparent background, and over a picture.
	for _, bg := range []color.Color{color.NRGBA{}, color.NRGBA{40, 200, 90, 255}} {
		var anim refwebp.WEBP
		for i := range 8 {
			img := image.NewNRGBA(image.Rect(0, 0, 24, 16))
			draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
			draw.Draw(img, image.Rect(i*2, i, i*2+6, i+6), image.NewUniform(color.NRGBA{uint8(30 * i), 0, 200, 255}), image.Point{}, draw.Src)
			anim.Image, anim.Delay = append(anim.Image, img), append(anim.Delay, 40+10*i)
		}
		var b bytes.Buffer
		if err := refwebp.EncodeAll(&b, &anim, refwebp.Options{Lossless: true, Exact: true}); err != nil {
			t.Fatal(err)
		}
		sameAsLibwebp(t, fmt.Sprintf("libwebp's encoder, background %v", bg), b.Bytes(), 0)
	}
}

// Every frame is composited, in order; metadata after the frames, and
// chunks it doesn't know, are passed over; ANIM gives the loop count.
func TestCompositeAllFrames(t *testing.T) {
	var frames []webpTestFrame
	for i := range 10 {
		frames = append(frames, webpTestFrame{img: solid(2, 2, color.NRGBA{uint8(20 * i), 0, 0, 255}), ms: 20})
	}
	data := webpOf(t, 2, 2, frames, riffChunk("EXIF", []byte("Exif\x00\x00odd")), riffChunk("ABCD", nil))
	got, count, total, err := composited(data)
	if err != nil || count != 10 || total != 200*time.Millisecond || len(got) != 10 {
		t.Fatalf("composited = %d frames of %d, %v, %v", len(got), count, total, err)
	}
	for i, img := range decodedFrames(t, got) {
		if r := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA).R; r != uint8(20*i) {
			t.Fatalf("frame %d is red %d", i, r)
		}
	}
	for _, loops := range []int{0, 1, 3} {
		looped := append([]byte(nil), data...)
		binary.LittleEndian.PutUint16(looped[42:44], uint16(loops)) // ANIM's data starts at 38, after VP8X's 18 bytes
		if a, err := Read(looped); err != nil || a.Loops != loops {
			t.Fatalf("loops %d: %+v, %v", loops, a, err)
		}
	}
}

// A frame that is lossy, with its alpha in an ALPH chunk, decodes too.
func TestCompositeLossyWithAlpha(t *testing.T) {
	var anim refwebp.WEBP
	for i := range 4 {
		img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
		draw.Draw(img, image.Rect(i*4, i*4, i*4+16, i*4+16), image.NewUniform(color.NRGBA{200, 100, 0, 255}), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(i*4+2, i*4+2, i*4+6, i*4+6), image.NewUniform(color.NRGBA{200, 100, 0, 100}), image.Point{}, draw.Src)
		anim.Image, anim.Delay = append(anim.Image, img), append(anim.Delay, 50)
	}
	var b bytes.Buffer
	if err := refwebp.EncodeAll(&b, &anim, refwebp.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b.Bytes(), []byte("ALPH")) || !bytes.Contains(b.Bytes(), []byte("VP8 ")) {
		t.Fatal("libwebp wrote no lossy frame with alpha")
	}
	got, count, _, err := composited(b.Bytes())
	if err != nil || count != 4 {
		t.Fatalf("composited = %d frames, %v", count, err)
	}
	// Colors decode by x/image's upsampling, not libwebp's, but alpha is
	// kept losslessly: it is libwebp's to the bit.
	ref, err := refwebp.DecodeAll(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for i, img := range decodedFrames(t, got) {
		for y := range 32 {
			for x := range 32 {
				_, _, _, a := img.At(x, y).RGBA()
				_, _, _, w := ref.Image[i].At(x, y).RGBA()
				if a != w {
					t.Fatalf("frame %d at (%d,%d): alpha %d, libwebp %d", i, x, y, a>>8, w>>8)
				}
			}
		}
	}
	last := decodedFrames(t, got)[3]
	if _, _, _, a := last.At(1, 1).RGBA(); a != 0 {
		t.Fatalf("a transparent corner has alpha %d", a)
	}
	if r, _, _, a := last.At(20, 20).RGBA(); a>>8 != 255 || r>>8 < 180 {
		t.Fatalf("the square is %v", last.At(20, 20))
	}
}

// What is not a whole animated WebP is refused, never read past its end.
func TestRefuseBadFiles(t *testing.T) {
	good := webpOf(t, 4, 4, []webpTestFrame{{img: solid(2, 2, color.Black), ms: 10}})
	frame := func(head []byte, bits []byte) []byte {
		return riffFile(vp8xChunk(0x12, 4, 4), riffChunk("ANIM", make([]byte, 6)), riffChunk("ANMF", append(head, bits...)))
	}
	bits := losslessBits(t, solid(2, 2, color.Black))
	head := func(x, y, w, h int) []byte {
		return append(append(append(append(append(le24(x/2), le24(y/2)...), le24(w-1)...), le24(h-1)...), le24(10)...), 0)
	}
	short := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(short[4:8], uint32(len(good)))
	for name, data := range map[string][]byte{
		"empty":             nil,
		"a still extended":  riffFile(vp8xChunk(0x10, 4, 4), riffChunk("VP8L", bits)),
		"riff size too big": short,
		"chunk past end":    good[:len(good)-3],
		"short frame chunk": frame(head(0, 0, 2, 2)[:10], nil),
		"outside canvas":    frame(head(4, 0, 2, 2), bits),
		"no frames":         riffFile(vp8xChunk(0x12, 4, 4), riffChunk("ANIM", make([]byte, 6))),
		"short ANIM":        riffFile(vp8xChunk(0x12, 4, 4), riffChunk("ANIM", make([]byte, 4))),
		"no bitstream":      frame(head(0, 0, 2, 2), riffChunk("ALPH", []byte{0})),
		"size disagrees":    frame(head(0, 0, 4, 4), bits),
		"bad bitstream":     frame(head(0, 0, 2, 2), riffChunk("VP8L", []byte{0x2f, 1, 2, 3})),
	} {
		if _, _, _, err := composited(data); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Whatever its input, Read and Composite return or fail; they never
// panic.
func FuzzComposite(f *testing.F) {
	f.Add(webpOf(f, 4, 4, []webpTestFrame{
		{img: solid(4, 4, color.NRGBA{255, 0, 0, 255}), ms: 10},
		{img: solid(2, 2, color.NRGBA{0, 0, 255, 128}), at: image.Pt(2, 2), ms: 10, blend: true, dispose: true},
	}))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _, _ = composited(data)
	})
}
