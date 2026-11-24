// Package media is the contract of the media service: descriptions of the
// images, animations, and videos people send, kept by content. The media
// module provides it as Service, describing media itself as messages
// arrive; a module that reads descriptions lists Service in its
// descriptor's Requires and looks it up with otogi.Lookup[Describer].
package media

import (
	"context"
	"errors"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Service is the name the media module provides Describer under.
const Service = "media"

// Kind says what a piece of media is.
type Kind string

// The kinds of media a description gives.
const (
	KindScreenshot Kind = "screenshot"
	KindPhoto      Kind = "photo"
	KindSticker    Kind = "sticker"
	KindMeme       Kind = "meme"
	KindVideo      Kind = "video"
	KindOther      Kind = "other"
)

// Description says what a piece of media shows.
type Description struct {
	// Kind is what the media is.
	Kind Kind
	// Summary is what it shows and, for a sticker or meme, what it conveys,
	// in the configured language.
	Summary string
	// Text is the text on it, or what a video says and its captions,
	// verbatim; empty when there is none.
	Text string
}

var (
	// ErrNotAllowed reports media of a chat that is not on the media
	// module's describe list.
	ErrNotAllowed = errors.New("media: chat not on the describe list")
	// ErrNotDescribed reports media of a kind or size that is not described.
	ErrNotDescribed = errors.New("media: not described")
)

// Describer gives the descriptions the media module keeps. The module
// describes media itself, as messages arrive; callers only wait for or
// look up the result. It is safe for concurrent use.
type Describer interface {
	// Describe returns the description of m, seen in from, waiting until
	// ctx ends for the module to finish with it. It fails with
	// ErrNotAllowed or ErrNotDescribed without waiting, with
	// otogi.ErrUnavailable when every configured model failed, and with
	// ctx's error when the wait ran out.
	Describe(ctx context.Context, from otogi.Destination, m otogi.Media) (Description, error)
	// Lookup returns the kept description of m, seen in from, without
	// waiting; ok is false when there is none.
	Lookup(ctx context.Context, from otogi.Destination, m otogi.Media) (d Description, ok bool, err error)
}
