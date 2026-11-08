package cardimage

import (
	_ "embed"
	"fmt"
	"html/template"
	"strconv"
)

// Pictures says how cards show the pictures they must not show plainly.
// The censor module provides it as the service ServicePictures, from its
// settings; card modules require it and draw with its Funcs, so one place
// controls how every card censors. A card's template, given Funcs, puts
// {{pictureStyles}} in its <style> and uses:
//
//   - Inside an element of class "blurred", pictures are blurred enough to
//     hide their details but not what they show: for content its source
//     marks sensitive, such as an X post's sensitive media or an R-18 BOOTH
//     item.
//   - {{cover}} inside a picture's positioned box puts the sticker over it,
//     see-through: for a picture the platform refused for what it shows
//     (otogi.ErrRefused), when CoverRefused is set, so the card can go out
//     again.
type Pictures struct {
	// Blur is how far sensitive pictures are blurred, in CSS pixels of a
	// card's layout; 0 shows them plainly.
	Blur int
	// CoverRefused sends a card the platform refused for its pictures again,
	// with the sticker over each picture.
	CoverRefused bool
	// Sticker is the sticker image, as a data: URI.
	Sticker template.URL
	// StickerOpacity is the sticker's opacity, from 0 to 1.
	StickerOpacity float64
	// StickerSize is how much of a picture the sticker spans, in percent of
	// its width and height.
	StickerSize int
}

// ServicePictures names the service through which the censor module offers
// Pictures.
const ServicePictures = "cardimage.pictures"

//go:embed cover.webp
var builtinSticker []byte

// DefaultPictures is how cards censor unless the censor module's settings
// say otherwise: a light blur, and refused cards covered by the built-in
// sticker.
func DefaultPictures() Pictures {
	return Pictures{Blur: 9, CoverRefused: true, Sticker: BuiltinSticker(), StickerOpacity: .65, StickerSize: 66}
}

// BuiltinSticker is the sticker cards use unless another is configured.
func BuiltinSticker() template.URL { return DataURI(builtinSticker, "image/webp") }

// Funcs returns the template functions for showing pictures less than
// plainly: pictureStyles and cover. The styles' selectors carry two
// classes, so they outweigh a template's own rules for its pictures, such
// as ".item img", wherever the template puts them. A blurred picture is
// scaled up a little so the blur does not fade at its edges.
func (p Pictures) Funcs() template.FuncMap {
	styles := fmt.Sprintf(`
  img.cover.sticker { position: absolute; inset: 0; margin: auto; width: %d%%; height: %d%%; max-width: none; max-height: none; object-fit: contain; opacity: %s; filter: none; transform: none; }
`, p.StickerSize, p.StickerSize, strconv.FormatFloat(p.StickerOpacity, 'f', -1, 64))
	if p.Blur > 0 {
		styles += fmt.Sprintf("  .blurred img:not(.cover) { filter: blur(%dpx); transform: scale(1.06); }\n", p.Blur)
	}
	cover := template.HTML(`<img class="cover sticker" src="` + string(p.Sticker) + `" alt="">`)
	return template.FuncMap{
		"pictureStyles": func() template.CSS { return template.CSS(styles) },
		"cover":         func() template.HTML { return cover },
	}
}
