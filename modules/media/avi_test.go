package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"testing"
)

// aviFile is an AVI as aviOf writes it, read back.
type aviFile struct {
	microSecPerFrame, frames, width, height uint32
	scale, rate, length                     uint32
	handler, compression                    string
	chunks                                  [][]byte // the 00dc chunks of movi, in order
	index                                   [][3]uint32
	moviAt                                  int // where movi's kind is, which idx1 offsets count from
}

// readAVI reads an AVI's RIFF structure, failing t on anything out of
// place: a size past the end, a chunk outside its list, a missing header.
func readAVI(t testing.TB, data []byte) aviFile {
	t.Helper()
	u32 := func(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "AVI " || int(u32(data[4:8])) != len(data)-8 {
		t.Fatalf("not a whole RIFF AVI: %q, size %d of %d", data[:min(12, len(data))], u32(data[4:8]), len(data)-8)
	}
	var a aviFile
	var walk func(at, end int)
	walk = func(at, end int) {
		for at < end {
			if end-at < 8 {
				t.Fatalf("a chunk header cut at %d", at)
			}
			id, n := string(data[at:at+4]), int(u32(data[at+4:at+8]))
			body := at + 8
			if body+n > end {
				t.Fatalf("chunk %q at %d runs past its list", id, at)
			}
			c := data[body : body+n]
			switch id {
			case "LIST":
				if string(c[:4]) == "movi" {
					a.moviAt = body
				}
				walk(body+4, body+n)
			case "avih":
				a.microSecPerFrame, a.frames, a.width, a.height = u32(c[0:]), u32(c[16:]), u32(c[32:]), u32(c[36:])
			case "strh":
				a.handler, a.scale, a.rate, a.length = string(c[4:8]), u32(c[20:]), u32(c[24:]), u32(c[32:])
			case "strf":
				a.compression = string(c[16:20])
			case "00dc":
				a.chunks = append(a.chunks, c)
			case "idx1":
				for i := 0; i+16 <= len(c); i += 16 {
					if string(c[i:i+4]) != "00dc" {
						t.Fatalf("index entry %d is %q", i/16, c[i:i+4])
					}
					a.index = append(a.index, [3]uint32{u32(c[i+4:]), u32(c[i+8:]), u32(c[i+12:])})
				}
			}
			at = body + n + n%2
		}
	}
	walk(12, len(data))
	return a
}

// jpegOf is a w x h JPEG of c.
func jpegOf(t testing.TB, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// An AVI shows each frame for a second, a Motion JPEG stream of the
// frames' size, its index pointing at each frame's chunk.
func TestAVI(t *testing.T) {
	frames := [][]byte{jpegOf(t, 6, 4, color.RGBA{255, 0, 0, 255}), jpegOf(t, 6, 4, color.RGBA{0, 255, 0, 255}), jpegOf(t, 6, 4, color.RGBA{0, 0, 255, 255})}
	if len(frames[1])%2 == 0 { // an odd-sized chunk, padded; JPEG decoders stop at the end marker
		frames[1] = append(frames[1], 0)
	}
	data, err := aviOf(frames)
	if err != nil {
		t.Fatal(err)
	}
	a := readAVI(t, data)
	if a.microSecPerFrame != 1_000_000 || a.frames != 3 || a.width != 6 || a.height != 4 ||
		a.handler != "MJPG" || a.compression != "MJPG" || a.scale != 1 || a.rate != 1 || a.length != 3 {
		t.Fatalf("headers %+v", a)
	}
	if len(a.chunks) != 3 || len(a.index) != 3 {
		t.Fatalf("%d chunks, %d index entries", len(a.chunks), len(a.index))
	}
	for i, c := range a.chunks {
		if !bytes.Equal(c, frames[i]) {
			t.Fatalf("frame %d differs", i)
		}
		if img, err := jpeg.Decode(bytes.NewReader(c)); err != nil || img.Bounds().Size() != image.Pt(6, 4) {
			t.Fatalf("frame %d: %v", i, err)
		}
		e := a.index[i]
		at := a.moviAt + int(e[1])
		if e[0] != 0x10 || string(data[at:at+4]) != "00dc" || binary.LittleEndian.Uint32(data[at+4:]) != e[2] || int(e[2]) != len(frames[i]) {
			t.Fatalf("index entry %d %v points at %q", i, e, data[at:at+4])
		}
	}
	if _, err := aviOf(nil); err == nil {
		t.Fatal("an AVI of no frames")
	}
}

// A frame for a video is on white, and no larger than maxFrameSide.
func TestJPEGFrame(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 2048, 1024))
	draw.Draw(canvas, image.Rect(1024, 0, 2048, 1024), image.NewUniform(color.RGBA{200, 0, 0, 255}), image.Point{}, draw.Src)
	data, err := jpegFrame(canvas)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Size() != image.Pt(1024, 512) {
		t.Fatalf("frame %v, %v", img.Bounds(), err)
	}
	near := func(c color.Color, r, g, b uint8) bool {
		got := color.RGBAModel.Convert(c).(color.RGBA)
		d := func(x, y uint8) int { return max(int(x), int(y)) - min(int(x), int(y)) }
		return d(got.R, r) < 12 && d(got.G, g) < 12 && d(got.B, b) < 12
	}
	if !near(img.At(100, 256), 255, 255, 255) || !near(img.At(900, 256), 200, 0, 0) {
		t.Fatalf("transparent reads %v, red %v", img.At(100, 256), img.At(900, 256))
	}
	small := image.NewRGBA(image.Rect(0, 0, 30, 20))
	if data, err := jpegFrame(small); err != nil {
		t.Fatal(err)
	} else if cfg, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != 30 || cfg.Height != 20 {
		t.Fatalf("a small frame is %+v, %v", cfg, err)
	}
}
