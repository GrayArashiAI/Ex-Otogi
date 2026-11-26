package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/TundraWork/Ex-Otogi/pkg/webpanim"
	refwebp "github.com/gen2brain/webp"
)

// animatedWebP is a lossless animated WebP of 4x4 frames, one of each
// color, each shown ms milliseconds, as libwebp's encoder makes it.
func animatedWebP(t testing.TB, ms int, colors ...color.Color) []byte {
	t.Helper()
	var anim refwebp.WEBP
	for _, c := range colors {
		img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
		for i := 0; i < len(img.Pix); i += 4 {
			r, g, b, a := c.RGBA()
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(r>>8), uint8(g>>8), uint8(b>>8), uint8(a>>8)
		}
		anim.Image, anim.Delay = append(anim.Image, img), append(anim.Delay, ms)
	}
	var b bytes.Buffer
	if err := refwebp.EncodeAll(&b, &anim, refwebp.Options{Lossless: true, Exact: true}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// n frames are spread from the first to the last, as a GIF's are.
func TestWebPFramesSpread(t *testing.T) {
	var colors []color.Color
	for i := range 10 {
		colors = append(colors, color.NRGBA{uint8(20 * i), 0, 0, 255})
	}
	got, count, total, err := webpFrames(animatedWebP(t, 20, colors...), 6)
	if err != nil || count != 10 || total != 200*time.Millisecond || len(got) != 6 {
		t.Fatalf("webpFrames = %d frames of %d, %v, %v", len(got), count, total, err)
	}
	var reds []uint8
	for _, f := range got {
		img, err := png.Decode(bytes.NewReader(f))
		if err != nil {
			t.Fatal(err)
		}
		reds = append(reds, color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA).R)
	}
	if want := []uint8{0, 40, 80, 100, 140, 180}; !bytes.Equal(reds, want) {
		t.Fatalf("frames picked = %v, want %v", reds, want)
	}
}

// An animation too large to decode is refused before any frame is
// decoded: by its canvas and largest frame, or by its frames' pixels in
// all. Neither file has a frame that would decode.
func TestWebPFramesTooLarge(t *testing.T) {
	chunk := func(id string, c []byte) []byte {
		return append(append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(c)))...), c...)
	}
	le24 := func(n int) []byte { return []byte{byte(n), byte(n >> 8), byte(n >> 16)} }
	file := func(w, h int, frames ...[]byte) []byte {
		body := append([]byte("WEBP"), chunk("VP8X", append(append([]byte{0x12, 0, 0, 0}, le24(w-1)...), le24(h-1)...))...)
		body = append(body, chunk("ANIM", make([]byte, 6))...)
		for _, f := range frames {
			body = append(body, f...)
		}
		return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
	}
	anmf := func(w, h int) []byte {
		head := append(append(append(append(append(le24(0), le24(0)...), le24(w-1)...), le24(h-1)...), le24(10)...), 0)
		return chunk("ANMF", append(head, chunk("VP8L", []byte{1, 2, 3, 0})...))
	}
	var many [][]byte
	for range maxWebPPixels/(1024*1024) + 1 {
		many = append(many, anmf(1024, 1024))
	}
	for name, data := range map[string][]byte{"canvas": file(6000, 6000, anmf(1, 1)), "pixels": file(1024, 1024, many...)} {
		if _, _, _, err := webpFrames(data, 6); !errors.Is(err, errTooLarge) {
			t.Errorf("%s: %v, want errTooLarge", name, err)
		}
	}
}

// An animated WebP goes to the model as frames, as a GIF does.
func TestAnimatedWebPIsSentAsFrames(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	a := h.image("https://qq/anim.webp", animatedWebP(t, 100, color.NRGBA{0, 0, 0, 255}, color.NRGBA{60, 0, 0, 255}, color.NRGBA{120, 0, 0, 255}, color.NRGBA{180, 0, 0, 255}))
	a.Role, a.MediaType = otogi.MediaAnimation, "image/webp"
	if err := h.handle(a); err != nil {
		t.Fatal(err)
	}
	parts := h.steps[0].requests[0].Turns[0].Content.Parts
	if len(parts) != 5 || !strings.Contains(parts[0].Text, "4 frames") || !strings.Contains(parts[0].Text, "0.4 seconds") || parts[1].Media.MediaType != "image/png" {
		t.Fatalf("animation sent as %d parts, first %q", len(parts), parts[0].Text)
	}
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	if p, _ := h.host.records[0].Payload.(map[string]any); p["frames"] != 4 {
		t.Fatalf("frames recorded = %v, want 4", p["frames"])
	}
}

// BenchmarkWebPFrames measures how fast frames decode and composite, for
// maxWebPPixels: twenty lossy 512x512 frames, as libwebp's encoder makes
// them.
func BenchmarkWebPFrames(b *testing.B) {
	var anim refwebp.WEBP
	for i := range 20 {
		img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
		for y := range 512 {
			for x := range 512 {
				img.Set(x, y, color.NRGBA{uint8(x + 7*i), uint8(y ^ x), uint8(y + 3*i), 255})
			}
		}
		anim.Image, anim.Delay = append(anim.Image, img), append(anim.Delay, 50)
	}
	var data bytes.Buffer
	if err := refwebp.EncodeAll(&data, &anim, refwebp.Options{Quality: 75}); err != nil {
		b.Fatal(err)
	}
	a, err := webpanim.Read(data.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	pixels := a.Pixels()
	runs := 0
	for b.Loop() {
		if _, _, _, err := webpFrames(data.Bytes(), animationFrames); err != nil {
			b.Fatal(err)
		}
		runs++
	}
	b.ReportMetric(float64(pixels)*float64(runs)/b.Elapsed().Seconds()/1e6, "Mpx/s")
}
