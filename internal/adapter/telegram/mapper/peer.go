package mapper

import (
	"fmt"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// PeerID converts a Telegram peer into the canonical protocol-neutral tagged identifier.
func PeerID(peer tg.PeerClass) (string, error) {
	switch value := peer.(type) {
	case *tg.PeerUser:
		if value == nil || value.UserID <= 0 {
			break
		}
		return fmt.Sprintf("user:%d", value.UserID), nil
	case *tg.PeerChat:
		if value == nil || value.ChatID <= 0 {
			break
		}
		return fmt.Sprintf("chat:%d", value.ChatID), nil
	case *tg.PeerChannel:
		if value == nil || value.ChannelID <= 0 {
			break
		}
		return fmt.Sprintf("channel:%d", value.ChannelID), nil
	}
	return "", fmt.Errorf("invalid Telegram peer: %w", otogi.ErrInvalid)
}

func messageFlow(message *tg.Message) (otogi.Direction, string, error) {
	if message == nil {
		return "", "", fmt.Errorf("nil Telegram message: %w", otogi.ErrInvalid)
	}
	direction := otogi.DirectionIncoming
	if message.Out {
		direction = otogi.DirectionOutgoing
	}
	from, ok := message.GetFromID()
	if !ok {
		return direction, "", nil
	}
	actor, err := PeerID(from)
	if err != nil {
		return "", "", err
	}
	return direction, actor, nil
}

// Peers returns what the entities say of users and chats, by their peer
// IDs (see PeerID): a user's full name, or else username, and whether it is
// a bot; a group's or channel's title.
func Peers(users []tg.UserClass, chats []tg.ChatClass) map[string]store.Peer {
	peers := map[string]store.Peer{}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			name := strings.TrimSpace(user.FirstName + " " + user.LastName)
			if name == "" {
				name = user.Username
			}
			if name != "" || user.Bot {
				peers[fmt.Sprintf("user:%d", user.ID)] = store.Peer{Name: name, Bot: user.Bot}
			}
		}
	}
	for _, c := range chats {
		switch chat := c.(type) {
		case *tg.Chat:
			peers[fmt.Sprintf("chat:%d", chat.ID)] = store.Peer{Name: chat.Title}
		case *tg.Channel:
			peers[fmt.Sprintf("channel:%d", chat.ID)] = store.Peer{Name: chat.Title}
		}
	}
	return peers
}
