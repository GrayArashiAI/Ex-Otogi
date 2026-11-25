package mapper

import (
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/tg"
)

func TestKnownMediaGetsTypedProjectionAndRawExtension(t *testing.T) {
	cases := []struct {
		media tg.MessageMediaClass
		kind  otogi.PartKind
	}{
		{&tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: 12.5, Long: -34.25}}, otogi.PartLocation},
		{&tg.MessageMediaContact{FirstName: "Ada", LastName: "Lovelace", PhoneNumber: "+1"}, otogi.PartContact},
	}
	for _, tc := range cases {
		message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100, Media: tc.media}
		message.SetMedia(tc.media)
		mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 1, PtsCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		part := mapped.Events[0].Message.Parts[0]
		if part.Kind != tc.kind || len(part.Extensions) != 1 || part.Extensions[0].Name != "message-media" {
			t.Fatalf("part kind=%s extensions=%v", part.Kind, part.Extensions)
		}
	}
}

func TestDocumentAndPhotoUseOpaqueRefreshLocators(t *testing.T) {
	document := &tg.Document{ID: 10, AccessHash: 11, FileReference: []byte{12}, MimeType: "application/pdf", Size: 99, DCID: 4}
	docMedia := &tg.MessageMediaDocument{}
	docMedia.SetDocument(document)
	photo := &tg.Photo{ID: 20, AccessHash: 21, FileReference: []byte{22}, DCID: 5}
	photoMedia := &tg.MessageMediaPhoto{}
	photoMedia.SetPhoto(photo)
	for _, tc := range []struct {
		media tg.MessageMediaClass
		role  otogi.MediaRole
	}{{docMedia, otogi.MediaDocument}, {photoMedia, otogi.MediaImage}} {
		message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100}
		message.SetMedia(tc.media)
		mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 1, PtsCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		media := mapped.Events[0].Message.Parts[0].Media
		if media == nil || media.Role != tc.role || media.Source.Kind != otogi.MediaSourceOpaque {
			t.Fatalf("media role=%s source=%s", media.Role, media.Source.Kind)
		}
		locator := media.Source.Locator
		decoded, err := DecodeMediaLocator(locator.Data)
		if err != nil || decoded.ID == 0 || len(decoded.FileReference) == 0 {
			t.Fatalf("locator=%+v err=%v", decoded, err)
		}
	}
}

// The attributes are those Telegram sent the test deployment for each kind.
func TestStickersAndGIFsAreToldFromFiles(t *testing.T) {
	sticker := &tg.DocumentAttributeSticker{Alt: "😀", Stickerset: &tg.InputStickerSetEmpty{}}
	square := &tg.DocumentAttributeImageSize{W: 512, H: 512}
	name := &tg.DocumentAttributeFilename{FileName: "f"}
	for _, tc := range []struct {
		name       string
		mime       string
		attributes []tg.DocumentAttributeClass
		video      bool
		role       otogi.MediaRole
	}{
		{"static sticker", "image/webp", []tg.DocumentAttributeClass{square, sticker, name}, false, otogi.MediaSticker},
		{"animated sticker", "application/x-tgsticker", []tg.DocumentAttributeClass{square, sticker, name}, false, otogi.MediaSticker},
		{"video sticker", "video/webm", []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 512, H: 512, Duration: 1}, sticker, name}, false, otogi.MediaSticker},
		{"GIF", "video/mp4", []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 180, H: 180, Duration: 4.2, Nosound: true}, &tg.DocumentAttributeAnimated{}, name}, false, otogi.MediaAnimation},
		{"video", "video/mp4", []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 1232, H: 880, Duration: 7.2}, name}, true, otogi.MediaVideo},
		{"picture sent as a file", "image/png", []tg.DocumentAttributeClass{&tg.DocumentAttributeImageSize{W: 2560, H: 1440}, name}, false, otogi.MediaDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			media := &tg.MessageMediaDocument{Video: tc.video}
			media.SetDocument(&tg.Document{ID: 10, AccessHash: 11, FileReference: []byte{12}, MimeType: tc.mime, Size: 99, DCID: 4, Attributes: tc.attributes})
			message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100}
			message.SetMedia(media)
			mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 1, PtsCount: 1})
			if err != nil {
				t.Fatal(err)
			}
			got := mapped.Events[0].Message.Parts[0].Media
			if got.Role != tc.role || got.MediaType != tc.mime {
				t.Fatalf("role=%s type=%s, want %s %s", got.Role, got.MediaType, tc.role, tc.mime)
			}
		})
	}
}
