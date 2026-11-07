package mapper

import (
	"encoding/json"
	"fmt"

	"github.com/gotd/td/tg"
)

const (
	// NamespaceCommon is Telegram's account-wide message ID space, used by
	// private chats and basic groups.
	NamespaceCommon = "common"
	// NamespaceChannel is a per-channel message ID space, used by channels and
	// supergroups.
	NamespaceChannel = "channel"
)

// MessageLocator identifies a Telegram message; it is the Data of the
// ProviderLocator on events and relations.
type MessageLocator struct {
	// Namespace is NamespaceCommon or NamespaceChannel.
	Namespace string `json:"namespace"`
	// Peer is the conversation, as returned by PeerID.
	Peer string `json:"peer"`
	// MessageID is Telegram's message ID.
	MessageID int `json:"message_id"`
}

// MediaLocator identifies Telegram media so the driver can download it later.
type MediaLocator struct {
	// Kind is "photo" or "document".
	Kind string `json:"kind"`
	// ID is the media ID.
	ID int64 `json:"id"`
	// AccessHash authorizes access to the media.
	AccessHash int64 `json:"access_hash"`
	// FileReference is Telegram's expiring file reference.
	FileReference []byte `json:"file_reference"`
	// DCID is the data center holding the media.
	DCID int `json:"dc_id"`
	// ThumbSize selects the photo size to download: the largest one.
	ThumbSize string `json:"thumb_size,omitempty"`
}

// EncodeMessageLocator encodes a message locator.
func EncodeMessageLocator(l MessageLocator) ([]byte, error) { return marshal("message locator", l) }

// DecodeMessageLocator decodes a message locator.
func DecodeMessageLocator(data []byte) (MessageLocator, error) {
	var l MessageLocator
	return l, unmarshal("message locator", data, &l)
}

// EncodeMediaLocator encodes a media locator.
func EncodeMediaLocator(l MediaLocator) ([]byte, error) { return marshal("media locator", l) }

// DecodeMediaLocator decodes a media locator.
func DecodeMediaLocator(data []byte) (MediaLocator, error) {
	var l MediaLocator
	return l, unmarshal("media locator", data, &l)
}

func marshal(what string, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", what, err)
	}
	return data, nil
}

func unmarshal(what string, data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode %s: %w", what, err)
	}
	return nil
}

// InboxKey reports whether update is a kind the adapter persists and routes,
// and the key that identifies it in the inbox, so a delivery repeated is
// stored once. Within a box ("common" or "channel:<id>"), a new message is
// keyed by its message ID: gotd hands over messages it recovers from a
// difference with pts -1, and a message is new only once, whether pushed
// or recovered. Edits and deletions are keyed by their pts, which they
// always carry.
func InboxKey(update tg.UpdateClass) (key string, ok bool) {
	newMessage := func(box string, m tg.MessageClass) (string, bool) {
		return fmt.Sprintf("%s:msg:%d", box, m.GetID()), box != ""
	}
	at := func(box string, pts int) (string, bool) { return fmt.Sprintf("%s:%d", box, pts), box != "" }
	switch u := update.(type) {
	case *tg.UpdateNewMessage:
		return newMessage(NamespaceCommon, u.Message)
	case *tg.UpdateEditMessage:
		return at(NamespaceCommon, u.Pts)
	case *tg.UpdateDeleteMessages:
		return at(NamespaceCommon, u.Pts)
	case *tg.UpdateNewChannelMessage:
		return newMessage(channelBox(u.Message), u.Message)
	case *tg.UpdateEditChannelMessage:
		return at(channelBox(u.Message), u.Pts)
	case *tg.UpdateDeleteChannelMessages:
		return at(fmt.Sprintf("channel:%d", u.ChannelID), u.Pts)
	default:
		return "", false
	}
}

func channelBox(message tg.MessageClass) string {
	var peer tg.PeerClass
	switch m := message.(type) {
	case *tg.Message:
		peer = m.PeerID
	case *tg.MessageService:
		peer = m.PeerID
	case *tg.MessageEmpty:
		peer, _ = m.GetPeerID()
	}
	if channel, ok := peer.(*tg.PeerChannel); ok {
		return fmt.Sprintf("channel:%d", channel.ChannelID)
	}
	return ""
}
