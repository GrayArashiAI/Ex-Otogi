package censor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type host struct {
	otogi.Host
	provided map[string]any
}

func (h *host) Provide(name string, service any) error {
	h.provided[name] = service
	return nil
}

func ptr[T any](v T) *T { return &v }

// No settings is the defaults; each set field overrides its own, and the
// result is what the card modules get.
func TestSettings(t *testing.T) {
	t.Parallel()
	m, err := New(Config{})
	if err != nil || m.pictures != cardimage.DefaultPictures() {
		t.Fatalf("defaults %+v, %v", m, err)
	}
	png := filepath.Join(t.TempDir(), "sticker.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n...."), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err = New(Config{Blur: ptr(0), CoverRefused: ptr(false), Sticker: png, StickerOpacity: ptr(0.5), StickerSize: 40})
	if err != nil {
		t.Fatal(err)
	}
	h := &host{provided: map[string]any{}}
	if err := m.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	got := h.provided[cardimage.ServicePictures].(cardimage.Pictures)
	if got.Blur != 0 || got.CoverRefused || got.StickerOpacity != 0.5 || got.StickerSize != 40 || !strings.HasPrefix(string(got.Sticker), "data:image/png;base64,") {
		t.Fatalf("provided %+v", got)
	}
	if d := m.Descriptor(); d.ID != "censor" || len(d.Provides) != 1 || d.Provides[0] != cardimage.ServicePictures {
		t.Fatalf("descriptor %+v", d)
	}
}

func TestSettingsErrors(t *testing.T) {
	t.Parallel()
	text := filepath.Join(t.TempDir(), "notes.txt")
	_ = os.WriteFile(text, []byte("hello"), 0o600)
	for want, cfg := range map[string]Config{
		"blur 50":                {Blur: ptr(50)},
		"sticker_opacity 0":      {StickerOpacity: ptr(0.0)},
		"sticker_size 120":       {StickerSize: 120},
		"no such file":           {Sticker: "/nonexistent/sticker.png"},
		"not an image (text/pla": {Sticker: text},
	} {
		if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v, want %q", cfg, err, want)
		}
	}
}
