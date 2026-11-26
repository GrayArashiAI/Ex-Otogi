// Package webpanim reads animated WebP files and composites their frames,
// for modules and drivers that need an animation's pictures: its container
// is read here, and each frame's bitstream is decoded by
// golang.org/x/image/webp, which decodes still pictures only.
//
// The WebP container, as its specification gives it
// (https://developers.google.com/speed/webp/docs/riff_container): "RIFF",
// the file's size after these 8 bytes, "WEBP", then chunks, each a FourCC,
// its size, and its data, padded to an even size. An animated file starts
// with an extended header, "VP8X", whose flags' bit 0x02 says it is
// animated, then ANIM, then an ANMF chunk for each frame; ICCP, EXIF, and
// XMP may come before or after the frames.
package webpanim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"time"

	"golang.org/x/image/webp"
)

// Animated reports whether data, which content sniffing took for a WebP,
// is an animated one, or a WebP too short to tell: by the extended header,
// "RIFF", its size, "WEBP", the "VP8X" chunk and its size, then its flags.
// Content sniffing takes 14 bytes for a WebP; the flags are the 21st.
func Animated(data []byte) (animated, short bool) {
	if len(data) >= 16 && string(data[12:16]) != "VP8X" {
		return false, false // a simple file, lossy or lossless: one picture
	}
	if len(data) < 21 {
		return false, true
	}
	return data[20]&0x02 != 0, false
}

// Animation is an animated WebP as its chunks give it, its frames not yet
// decoded.
type Animation struct {
	// Canvas is the picture the frames are composited on.
	Canvas image.Rectangle
	// Loops is how many times the animation plays, 0 for ever.
	Loops  int
	frames []frame
}

// frame is one frame, as its ANMF chunk gives it.
type frame struct {
	rect     image.Rectangle // where on the canvas it goes
	duration time.Duration
	blend    bool   // alpha-blended onto the canvas; else it replaces what it covers
	dispose  bool   // its rectangle is cleared to transparent before the next frame
	data     []byte // its frame data: an ALPH and a VP8 chunk, or a VP8L chunk
}

// chunks calls each with every chunk of data, its FourCC and its data, in
// order, until each returns an error. It fails on a chunk that runs past
// data's end.
func chunks(data []byte, each func(id string, c []byte) error) error {
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

// Read reads an animated WebP's canvas, loop count, and frames from its
// chunks, without decoding the frames. It fails on data that is not a
// whole animated WebP: the RIFF size past the data's end, a chunk running
// past it, no extended header first, an ANIM or frame chunk too short, a
// frame outside the canvas, or no frames.
func Read(data []byte) (*Animation, error) {
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" ||
		string(data[12:16]) != "VP8X" || binary.LittleEndian.Uint32(data[16:20]) < 10 {
		return nil, errors.New("webp: not an extended webp")
	}
	if data[20]&0x02 == 0 {
		return nil, errors.New("webp: not animated")
	}
	size := int64(binary.LittleEndian.Uint32(data[4:8]))
	if size < 4 || size > int64(len(data)-8) {
		return nil, errors.New("webp: truncated")
	}
	a := &Animation{Canvas: image.Rect(0, 0, 1+u24(data[24:27]), 1+u24(data[27:30]))}
	err := chunks(data[12:8+size], func(id string, c []byte) error {
		switch id {
		case "ANIM": // a background color, a hint players may ignore, then the loop count
			if len(c) < 6 {
				return errors.New("animation chunk too short")
			}
			a.Loops = int(binary.LittleEndian.Uint16(c[4:6]))
		case "ANMF":
			if len(c) < 16 {
				return errors.New("frame chunk too short")
			}
			x, y := 2*u24(c[0:3]), 2*u24(c[3:6])
			r := image.Rect(x, y, x+1+u24(c[6:9]), y+1+u24(c[9:12]))
			if !r.In(a.Canvas) {
				return fmt.Errorf("frame %d outside the canvas", len(a.frames))
			}
			a.frames = append(a.frames, frame{rect: r, duration: time.Duration(u24(c[12:15])) * time.Millisecond,
				blend: c[15]&0x02 == 0, dispose: c[15]&0x01 != 0, data: c[16:]})
		}
		return nil // VP8X and metadata say nothing of the pictures
	})
	if err != nil {
		return nil, fmt.Errorf("webp: %w", err)
	}
	if len(a.frames) == 0 {
		return nil, errors.New("webp: no frames")
	}
	return a, nil
}

// Frames is how many frames the animation has.
func (a *Animation) Frames() int { return len(a.frames) }

// Duration is how long the animation plays once, the frames' durations as
// the file gives them.
func (a *Animation) Duration() time.Duration {
	var total time.Duration
	for _, f := range a.frames {
		total += f.duration
	}
	return total
}

// Pixels is how many pixels the frames have, all together: the work of
// decoding them.
func (a *Animation) Pixels() int64 {
	var n int64
	for _, f := range a.frames {
		n += int64(f.rect.Dx()) * int64(f.rect.Dy())
	}
	return n
}

// Largest is how many pixels the largest frame has.
func (a *Animation) Largest() int64 {
	var n int64
	for _, f := range a.frames {
		n = max(n, int64(f.rect.Dx())*int64(f.rect.Dy()))
	}
	return n
}

// Composite decodes the frames in order and composites them as the
// specification gives it, each blended onto the canvas or replacing what
// it covers, and cleared after it shows when it is disposed, the canvas
// starting transparent (the background color ANIM gives is a hint the
// specification lets players ignore, as libwebp's own decoder does). It
// calls each with every frame's index, the canvas as that frame shows it,
// which each must not keep, and the frame's duration. It holds the canvas
// and one decoded frame at a time.
func (a *Animation) Composite(each func(i int, canvas *image.RGBA, d time.Duration) error) error {
	canvas := image.NewRGBA(a.Canvas)
	for i, f := range a.frames {
		still, err := f.still()
		if err != nil {
			return fmt.Errorf("webp: frame %d: %w", i, err)
		}
		m, err := webp.Decode(bytes.NewReader(still))
		if err != nil {
			return fmt.Errorf("webp: frame %d: %w", i, err)
		}
		if m.Bounds().Size() != f.rect.Size() {
			return fmt.Errorf("webp: frame %d is %v, its chunk says %v", i, m.Bounds().Size(), f.rect.Size())
		}
		op := draw.Src
		if f.blend {
			op = draw.Over
		}
		draw.Draw(canvas, f.rect, m, m.Bounds().Min, op)
		if err := each(i, canvas, f.duration); err != nil {
			return err
		}
		if f.dispose {
			draw.Draw(canvas, f.rect, image.Transparent, image.Point{}, draw.Src)
		}
	}
	return nil
}

// still makes f's data a WebP of its own, for x/image/webp to decode: its
// VP8L chunk alone, its VP8 chunk alone, or its ALPH and VP8 chunks after an
// extended header that says the picture has alpha.
func (f frame) still() ([]byte, error) {
	var alph, bits []byte
	var lossless bool
	err := chunks(f.data, func(id string, c []byte) error {
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
