package cardimage

import (
	"html/template"
	"strings"
	"testing"
)

// The styles and the sticker follow the settings; no blur leaves sensitive
// pictures plain.
func TestPicturesFuncs(t *testing.T) {
	t.Parallel()
	styles := func(p Pictures) string { return string(p.Funcs()["pictureStyles"].(func() template.CSS)()) }
	p := DefaultPictures()
	if s := styles(p); !strings.Contains(s, "blur(9px)") || !strings.Contains(s, "width: 66%") || !strings.Contains(s, "opacity: 0.65") {
		t.Fatalf("default styles %s", s)
	}
	p.Blur, p.StickerSize, p.StickerOpacity, p.Sticker = 0, 40, 1, "data:image/png;base64,AA=="
	if s := styles(p); strings.Contains(s, "blur(") || !strings.Contains(s, "width: 40%") || !strings.Contains(s, "opacity: 1;") {
		t.Fatalf("styles %s", s)
	}
	if c := string(p.Funcs()["cover"].(func() template.HTML)()); c != `<img class="cover sticker" src="data:image/png;base64,AA==" alt="">` {
		t.Fatalf("cover %s", c)
	}
}
