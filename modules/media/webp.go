package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"time"

	"golang.org/x/image/webp"
)

// The WebP container, as its specification gives it
// (https://developers.google.com/speed/webp/docs/riff_container): "RIFF",
// the file's size after these 8 bytes, "WEBP", then chunks, each a FourCC,
// its size, and its data, padded to an even size. An animated file starts
// with an extended header, "VP8X", whose flags' bit 0x02 says it is
// animated, then ANIM, then an ANMF chunk for each frame; ICCP, EXIF, and
// XMP may come before or after the frames.

// animatedWebP reports whether data, which http.DetectContentType took for a
// WebP, is an animated one, or a WebP too short to tell: by the extended
// header, "RIFF", its size, "WEBP", the "VP8X" chunk and its size, then its
// flags. Content sniffing takes 14 bytes for a WebP; the flags are the 21st.
func animatedWebP(data []byte) (animated, short bool) {
	if len(data) >= 16 && string(data[12:16]) != "VP8X" {
		return false, false // a simple file, lossy or lossless: one picture
	}
	if len(data) < 21 {
		return false, true
	}
	return data[20]&0x02 != 0, false
}

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

// webpFrame is one frame of an animated WebP, as its ANMF chunk gives it.
type webpFrame struct {
	rect     image.Rectangle // where on the canvas it goes
	duration time.Duration
	blend    bool   // alpha-blended onto the canvas; else it replaces what it covers
	dispose  bool   // its rectangle is cleared to transparent before the next frame
	data     []byte // its frame data: an ALPH and a VP8 chunk, or a VP8L chunk
}

// webpChunks calls each with every chunk of data, its FourCC and its data,
// in order, until each returns an error. It fails on a chunk that runs
// past data's end.
func webpChunks(data []byte, each func(id string, c []byte) error) error {
	for i := 0; i < len(data); {
		if len(data)-i < 8 {
			return errors.New("truncated chunk header")
		}
		id, n := string(data[i:i+4]), int64(binary.LittleEndian.Uint32(data[i+4:i+8]))
		if n > int64(len(data)-i-8) {
			return fmt.Errorf("chunk %q runs past the end", id)
		}
		if err := each(id, data[i+8:i+8+int(n)]); err != nil {
			return err
		}
		i += 8 + int(n) + int(n&1)
	}
	return nil
}

// u24 reads a 24-bit little-endian number.
func u24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

// webpAnimation reads an animated WebP's canvas and frames from its chunks,
// without decoding the frames. It fails on data that is not a whole
// animated WebP: the RIFF size past the data's end, a chunk running past
// it, no extended header first, a frame chunk too short or outside the
// canvas, or no frames.
func webpAnimation(data []byte) (image.Rectangle, []webpFrame, error) {
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" ||
		string(data[12:16]) != "VP8X" || binary.LittleEndian.Uint32(data[16:20]) < 10 {
		return image.Rectangle{}, nil, errors.New("not an extended webp")
	}
	if data[20]&0x02 == 0 {
		return image.Rectangle{}, nil, errors.New("not animated")
	}
	size := int64(binary.LittleEndian.Uint32(data[4:8]))
	if size < 4 || size > int64(len(data)-8) {
		return image.Rectangle{}, nil, errors.New("truncated")
	}
	canvas := image.Rect(0, 0, 1+u24(data[24:27]), 1+u24(data[27:30]))
	var frames []webpFrame
	err := webpChunks(data[12:8+size], func(id string, c []byte) error {
		if id != "ANMF" {
			return nil // VP8X, ANIM, and metadata say nothing of the pictures
		}
		if len(c) < 16 {
			return errors.New("frame chunk too short")
		}
		x, y := 2*u24(c[0:3]), 2*u24(c[3:6])
		r := image.Rect(x, y, x+1+u24(c[6:9]), y+1+u24(c[9:12]))
		if !r.In(canvas) {
			return fmt.Errorf("frame %d outside the canvas", len(frames))
		}
		frames = append(frames, webpFrame{rect: r, duration: time.Duration(u24(c[12:15])) * time.Millisecond,
			blend: c[15]&0x02 == 0, dispose: c[15]&0x01 != 0, data: c[16:]})
		return nil
	})
	if err != nil {
		return image.Rectangle{}, nil, err
	}
	if len(frames) == 0 {
		return image.Rectangle{}, nil, errors.New("no frames")
	}
	return canvas, frames, nil
}

// still makes f's data a WebP of its own, for x/image/webp to decode: its
// VP8L chunk alone, its VP8 chunk alone, or its ALPH and VP8 chunks after an
// extended header that says the picture has alpha.
func (f webpFrame) still() ([]byte, error) {
	var alph, bits []byte
	var lossless bool
	err := webpChunks(f.data, func(id string, c []byte) error {
		switch {
		case bits != nil: // after the bitstream, unknown chunks
		case id == "ALPH":
			alph = c
		case id == "VP8 " || id == "VP8L":
			bits, lossless = c, id == "VP8L"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if bits == nil {
		return nil, errors.New("no bitstream")
	}
	var b bytes.Buffer
	chunk := func(id string, c []byte) {
		b.WriteString(id)
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c)))
		b.Write(c)
		if len(c)%2 == 1 {
			b.WriteByte(0)
		}
	}
	b.WriteString("WEBP")
	switch {
	case lossless:
		chunk("VP8L", bits)
	case alph != nil:
		w, h := f.rect.Dx()-1, f.rect.Dy()-1
		chunk("VP8X", []byte{0x10, 0, 0, 0, byte(w), byte(w >> 8), byte(w >> 16), byte(h), byte(h >> 8), byte(h >> 16)})
		chunk("ALPH", alph)
		chunk("VP8 ", bits)
	default:
		chunk("VP8 ", bits)
	}
	head := []byte("RIFF\x00\x00\x00\x00")
	binary.LittleEndian.PutUint32(head[4:], uint32(b.Len()))
	return append(head, b.Bytes()...), nil
}

// webpFrames decodes an animated WebP and composites its frames as its
// specification gives it, each blended onto the canvas or replacing what
// it covers, and cleared after it shows when it is disposed, the canvas
// starting transparent (the background color ANIM gives is a hint the
// specification lets players ignore, as libwebp's own decoder does). It
// returns n of them spread evenly from the first to the last as PNG, how
// many frames it has, and how long it plays, the frames' durations as the
// file gives them. An animation whose canvas and largest frame would take
// more than maxDecoded, or whose frames have more than maxWebPPixels in
// all, is not decoded: the error wraps errTooLarge.
func webpFrames(data []byte, n int) (frames [][]byte, count int, total time.Duration, err error) {
	bounds, all, err := webpAnimation(data)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode webp: %w", err)
	}
	var largest, pixels int64
	for _, f := range all {
		area := int64(f.rect.Dx()) * int64(f.rect.Dy())
		largest, pixels = max(largest, area), pixels+area
		total += f.duration
	}
	if size := 4 * (int64(bounds.Dx())*int64(bounds.Dy()) + largest); size > maxDecoded {
		return nil, 0, 0, fmt.Errorf("decode webp: %d MiB: %w", size>>20, errTooLarge)
	}
	if pixels > maxWebPPixels {
		return nil, 0, 0, fmt.Errorf("decode webp: %d million pixels: %w", pixels/1e6, errTooLarge)
	}
	count = len(all)
	want := spread(count, n)
	canvas := image.NewRGBA(bounds)
	for i, f := range all {
		still, err := f.still()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("decode webp: frame %d: %w", i, err)
		}
		m, err := webp.Decode(bytes.NewReader(still))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("decode webp: frame %d: %w", i, err)
		}
		if m.Bounds().Size() != f.rect.Size() {
			return nil, 0, 0, fmt.Errorf("decode webp: frame %d is %v, its chunk says %v", i, m.Bounds().Size(), f.rect.Size())
		}
		op := draw.Src
		if f.blend {
			op = draw.Over
		}
		draw.Draw(canvas, f.rect, m, m.Bounds().Min, op)
		if want[i] {
			var b bytes.Buffer
			if err := png.Encode(&b, canvas); err != nil {
				return nil, 0, 0, fmt.Errorf("encode frame %d: %w", i, err)
			}
			frames = append(frames, b.Bytes())
		}
		if f.dispose {
			draw.Draw(canvas, f.rect, image.Transparent, image.Point{}, draw.Src)
		}
	}
	return frames, count, total, nil
}
