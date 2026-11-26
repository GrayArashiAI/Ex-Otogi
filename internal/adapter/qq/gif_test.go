package qq

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"strings"
	"testing"

	refwebp "github.com/gen2brain/webp"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// animation is a lossless animated WebP of frames, as libwebp's encoder
// makes it, each shown for its delay in milliseconds, played loops times
// (0 for ever).
func animation(t testing.TB, loops int, delays []int, frames ...image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := refwebp.EncodeAll(&b, &refwebp.WEBP{Image: frames, Delay: delays, LoopCount: loops}, refwebp.Options{Lossless: true, Exact: true}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// tile is an 8x8 picture: transparent, a half transparent column at x 0
// (alpha 100, under the line), an opaque one at x 1 (alpha 200, over it),
// and an opaque square of c at (2,2)-(6,6).
func tile(c color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		img.SetNRGBA(0, y, color.NRGBA{200, 0, 0, 100})
		img.SetNRGBA(1, y, color.NRGBA{0, 200, 0, 200})
	}
	draw.Draw(img, image.Rect(2, 2, 6, 6), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// An animated WebP becomes a GIF of every frame: whole pictures disposed
// to the background, durations in hundredths of a second (at least 2),
// plays converted to image/gif's loop count, pixels at least half opaque
// drawn opaque and near their color, the rest transparent.
func TestGIFOf(t *testing.T) {
	colors := []color.NRGBA{{255, 0, 0, 255}, {0, 0, 255, 255}, {250, 200, 30, 255}}
	for _, c := range []struct{ plays, loopCount int }{{0, 0}, {1, -1}, {3, 2}} {
		data := animation(t, c.plays, []int{20, 5, 1004}, tile(colors[0]), tile(colors[1]), tile(colors[2]))
		out, err := gifOf(data)
		if err != nil {
			t.Fatal(err)
		}
		g, err := gif.DecodeAll(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Image) != 3 || g.LoopCount != c.loopCount || g.Delay[0] != 2 || g.Delay[1] != 2 || g.Delay[2] != 100 {
			t.Fatalf("plays %d: %d frames, loop count %d, delays %v", c.plays, len(g.Image), g.LoopCount, g.Delay)
		}
		for i, p := range g.Image {
			if g.Disposal[i] != gif.DisposalBackground || p.Bounds() != image.Rect(0, 0, 8, 8) {
				t.Fatalf("frame %d: disposal %d, bounds %v", i, g.Disposal[i], p.Bounds())
			}
			for y := range 8 {
				for x := range 8 {
					_, _, _, a := p.At(x, y).RGBA()
					opaque := x == 1 || image.Pt(x, y).In(image.Rect(2, 2, 6, 6))
					if opaque != (a == 0xffff) || !opaque && a != 0 {
						t.Fatalf("frame %d at (%d,%d): alpha %d", i, x, y, a>>8)
					}
				}
			}
			got := color.NRGBAModel.Convert(p.At(3, 3)).(color.NRGBA)
			if want := colors[i]; absDiff(got.R, want.R) > 8 || absDiff(got.G, want.G) > 8 || absDiff(got.B, want.B) > 8 {
				t.Fatalf("frame %d's square is %v, want about %v", i, got, want)
			}
		}
	}
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// An animation too large to convert is refused before any frame is
// decoded: by memory, the frames kept for the GIF included, or by its
// frames' pixels in all.
func TestGIFOfTooLarge(t *testing.T) {
	chunk := func(id string, c []byte) []byte {
		return append(append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(c)))...), c...)
	}
	le24 := func(n int) []byte { return []byte{byte(n), byte(n >> 8), byte(n >> 16)} }
	file := func(w, h, frames, fw, fh int) []byte {
		body := append([]byte("WEBP"), chunk("VP8X", append(append([]byte{0x12, 0, 0, 0}, le24(w-1)...), le24(h-1)...))...)
		body = append(body, chunk("ANIM", make([]byte, 6))...)
		head := append(append(append(append(append(le24(0), le24(0)...), le24(fw-1)...), le24(fh-1)...), le24(10)...), 0)
		for range frames {
			body = append(body, chunk("ANMF", append(head, chunk("VP8L", []byte{1, 2, 3, 0})...))...)
		}
		return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
	}
	for name, data := range map[string][]byte{
		"memory, frames kept": file(1024, 1024, 80, 1, 1), // 80 MiB of paletted frames, 4 of canvas
		"pixels":              file(512, 512, maxGIFPixels/(512*512)+1, 512, 512),
	} {
		if _, err := gifOf(data); !errors.Is(err, errGIFTooLarge) {
			t.Errorf("%s: %v, want errGIFTooLarge", name, err)
		}
	}
}

// Sent to QQ, an inline animated WebP is uploaded as a GIF, named .gif;
// a still WebP, or media QQ fetches itself, goes as it is.
func TestAnimatedWebPUploadsAsGIF(t *testing.T) {
	x, srv, _ := newEffects(t)
	var files []map[string]any
	srv.fail = func(path string, body map[string]any) (int, string, bool) {
		if strings.HasSuffix(path, "/files") {
			files = append(files, body)
		}
		return 0, "", false
	}
	animated := animation(t, 0, []int{100, 100}, tile(color.NRGBA{255, 0, 0, 255}), tile(color.NRGBA{0, 0, 255, 255}))
	var still bytes.Buffer
	if err := refwebp.Encode(&still, tile(color.NRGBA{255, 0, 0, 255}), refwebp.Options{Lossless: true}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []otogi.Media{
		{Role: otogi.MediaImage, MediaType: "image/webp", Filename: "00FBAAFA.webp", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: animated}},
		{Role: otogi.MediaImage, MediaType: "image/webp", Filename: "still.webp", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: still.Bytes()}},
		{Role: otogi.MediaImage, MediaType: "image/webp", Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: "https://example.com/a.webp"}},
	} {
		msg := otogi.Message{Roots: []otogi.PartID{"m"}, Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &m}}}
		if _, err := sendText(x, string(rune('a'+i)), "", "group:G1", msg); err != nil {
			t.Fatalf("media %d: %v", i, err)
		}
	}
	if len(files) != 3 {
		t.Fatalf("uploads %v", files)
	}
	data, _ := base64.StdEncoding.DecodeString(files[0]["file_data"].(string))
	if g, err := gif.DecodeAll(bytes.NewReader(data)); err != nil || len(g.Image) != 2 || files[0]["file_name"] != "00FBAAFA.gif" {
		t.Fatalf("animated: %v, %v", files[0]["file_name"], err)
	}
	if data, _ := base64.StdEncoding.DecodeString(files[1]["file_data"].(string)); !bytes.Equal(data, still.Bytes()) || files[1]["file_name"] != "still.webp" {
		t.Fatalf("still: %v", files[1]["file_name"])
	}
	if files[2]["url"] != "https://example.com/a.webp" {
		t.Fatalf("external: %v", files[2])
	}
}

// An animated WebP too large to convert is not uploaded, and the send
// fails as invalid, not to be tried again.
func TestAnimatedWebPTooLargeNotUploaded(t *testing.T) {
	x, srv, _ := newEffects(t)
	frames := make([]image.Image, 0, 80)
	delays := make([]int, 0, 80)
	for i := range 80 { // 80 frames of 1024x1024: 80 MiB kept as paletted frames
		img := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
		img.Pix[0] = uint8(i)
		frames, delays = append(frames, img), append(delays, 50)
	}
	m := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: animation(t, 0, delays, frames...)}}
	msg := otogi.Message{Roots: []otogi.PartID{"m"}, Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &m}}}
	if _, err := sendText(x, "big", "", "group:G1", msg); !errors.Is(err, otogi.ErrInvalid) || !errors.Is(err, errGIFTooLarge) {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(strings.Join(srv.paths, "\n"), "/files") {
		t.Fatalf("paths %v", srv.paths)
	}
}

// BenchmarkGIFOf measures how fast an animated WebP converts, for
// maxGIFPixels: twenty lossy 512x512 frames, as libwebp's encoder makes
// them.
func BenchmarkGIFOf(b *testing.B) {
	var frames []image.Image
	var delays []int
	for i := range 20 {
		img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
		for y := range 512 {
			for x := range 512 {
				img.Set(x, y, color.NRGBA{uint8(x + 7*i), uint8(y ^ x), uint8(y + 3*i), 255})
			}
		}
		frames, delays = append(frames, img), append(delays, 50)
	}
	var data bytes.Buffer
	if err := refwebp.EncodeAll(&data, &refwebp.WEBP{Image: frames, Delay: delays}, refwebp.Options{Quality: 75}); err != nil {
		b.Fatal(err)
	}
	runs := 0
	for b.Loop() {
		if _, err := gifOf(data.Bytes()); err != nil {
			b.Fatal(err)
		}
		runs++
	}
	b.ReportMetric(float64(20*512*512)*float64(runs)/b.Elapsed().Seconds()/1e6, "Mpx/s")
}
