package cardimage

import (
	_ "embed"
	"html/template"
)

// A card shows some pictures less than plainly. Its template, given
// Funcs, puts {{pictureStyles}} in its <style> and uses:
//
//   - Inside an element of class "blurred", pictures are blurred enough to
//     hide their details but not what they show: for content its source
//     marks sensitive, such as an X post's sensitive media or an R-18 BOOTH
//     item.
//   - {{cover}} inside a picture's positioned box puts the cover sticker
//     over it, see-through: for a picture the platform refused for what it
//     shows (otogi.ErrRefused), so the card can go out again.

//go:embed cover.webp
var coverSticker []byte

// pictureStyles is the CSS for the classes above. Its selectors carry two
// classes, so they outweigh a template's own rules for its pictures, such
// as ".item img", wherever the template puts them. A blurred picture is
// scaled up a little so the blur does not fade at its edges.
const pictureStyles template.CSS = `
  .blurred img:not(.cover) { filter: blur(9px); transform: scale(1.06); }
  img.cover.sticker { position: absolute; inset: 0; margin: auto; width: 66%; height: 66%; max-width: none; max-height: none; object-fit: contain; opacity: .8; filter: none; transform: none; }
`

var coverImage = template.HTML(`<img class="cover sticker" src="` + string(DataURI(coverSticker, "image/webp")) + `" alt="">`)

// Funcs returns the template functions for showing pictures less than
// plainly: pictureStyles and cover.
func Funcs() template.FuncMap {
	return template.FuncMap{
		"pictureStyles": func() template.CSS { return pictureStyles },
		"cover":         func() template.HTML { return coverImage },
	}
}
