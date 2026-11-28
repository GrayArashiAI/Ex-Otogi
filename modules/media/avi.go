package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/jpeg"
)

// aviOf writes frames, JPEG pictures of one size, as a Motion JPEG AVI that
// shows each for a second: the RIFF AVI layout Microsoft documents (AVI
// RIFF File Reference), its headers (avih, and one video stream's strh and
// strf, a BITMAPINFOHEADER with the MJPG codec), the frames as 00dc chunks
// in movi, and their index, idx1.
//
// Gemini samples a video at a frame a second, and its Interactions API
// takes no other rate, so a frame a second has the model see every frame;
// an AVI of JPEG needs no video encoder.
func aviOf(frames [][]byte) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("avi: no frames")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(frames[0]))
	if err != nil {
		return nil, err
	}
	w, h, n := uint32(cfg.Width), uint32(cfg.Height), uint32(len(frames))
	largest := 0
	for _, f := range frames {
		largest = max(largest, len(f))
	}
	var b bytes.Buffer
	put := func(vs ...any) {
		for _, v := range vs {
			switch v := v.(type) {
			case string:
				b.WriteString(v)
			default:
				_ = binary.Write(&b, binary.LittleEndian, v)
			}
		}
	}
	// list starts a RIFF or LIST of kind and returns a function that fills
	// in its size once its content is written.
	list := func(id, kind string) func() {
		put(id, uint32(0), kind)
		at := b.Len() - 8
		return func() { binary.LittleEndian.PutUint32(b.Bytes()[at:], uint32(b.Len()-at-4)) }
	}
	chunk := func(id string, vs ...any) {
		put(id, uint32(0))
		at := b.Len() - 4
		put(vs...)
		binary.LittleEndian.PutUint32(b.Bytes()[at:], uint32(b.Len()-at-4))
	}

	riff := list("RIFF", "AVI ")
	hdrl := list("LIST", "hdrl")
	const second = uint32(1_000_000) // microseconds a frame
	const hasIndex = uint32(0x10)
	chunk("avih", second, uint32(largest), uint32(0), hasIndex, n, uint32(0), uint32(1), uint32(largest),
		w, h, [4]uint32{})
	strl := list("LIST", "strl")
	// strh: a video stream of MJPG, a frame (scale 1) a second (rate 1).
	chunk("strh", "vids", "MJPG", uint32(0), uint16(0), uint16(0), uint32(0),
		uint32(1), uint32(1), uint32(0), n, uint32(largest), ^uint32(0), uint32(0),
		[4]int16{0, 0, int16(w), int16(h)})
	// strf: a BITMAPINFOHEADER, 24 bits a pixel, compressed as MJPG.
	chunk("strf", uint32(40), int32(w), int32(h), uint16(1), uint16(24), "MJPG", w*h*3,
		int32(0), int32(0), uint32(0), uint32(0))
	strl()
	hdrl()

	movi := list("LIST", "movi")
	moviAt := b.Len() - 4 // idx1's offsets count from the "movi" kind
	offsets := make([]uint32, 0, n)
	for _, f := range frames {
		offsets = append(offsets, uint32(b.Len()-moviAt))
		chunk("00dc", f)
		if len(f)%2 == 1 {
			b.WriteByte(0)
		}
	}
	movi()

	const keyFrame = uint32(0x10)
	put("idx1", n*16)
	for i, f := range frames {
		put("00dc", keyFrame, offsets[i], uint32(len(f)))
	}
	riff()
	return b.Bytes(), nil
}
