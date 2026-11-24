package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// cleanupTimeout bounds finishing a reply after its context ended.
const cleanupTimeout = 30 * time.Second

// respond has p answer e, continuing from turn parent (0 for a new thread):
// the reply streams into the chat, and the turns are kept. What the model
// first says before calling tools goes out as its own message before the
// tools run, so the chat sees it at once even where nothing streams (QQ
// groups); the rest of the reply, words before later tools included, follows
// in one more message. j is the judgment that woke p, if one did; until is
// how long the message may wait for its media's descriptions.
func (m *Module) respond(ctx context.Context, e otogi.Event, inv otogi.Invocation, p *profile, parent int64, trigger string, j *judgment, until time.Time) error {
	started := m.now()
	path, err := m.thread(ctx, parent, p.MaxTurns)
	if err != nil {
		return err
	}
	content, named := m.userContent(ctx, e, inv, p, parent == 0, until)
	user := llm.Turn{Role: llm.RoleUser, Content: content}
	// What leads the message, the notes on the people in it and the recent
	// chat, goes in this request only: the thread keeps the message alone.
	sent := llm.Turn{Role: llm.RoleUser, Content: user.Content}
	var recent string
	if p.context > 0 {
		var inRecent []sender
		if recent, inRecent, err = m.contextText(ctx, e, p, path); err != nil {
			return err
		}
		named = append(inRecent, named...)
	}
	// The stickers the agent may send lead too, in this request only: a
	// change to them leaves the cached part of the request alone.
	picks := m.stickersFor(ctx, p)
	var lead []string
	if picks != nil {
		lead = append(lead, picks.text())
	}
	for _, s := range []string{m.aboutPeople(e.Platform, named), recent} {
		if s != "" {
			lead = append(lead, s)
		}
	}
	if len(lead) > 0 {
		sent.Content = llm.Join(otogi.TextMessage(strings.Join(lead, "\n\n")), user.Content)
	}
	req := llm.Request{Model: p.Model, System: m.system(p, e), Thinking: p.Thinking, MaxOutput: p.MaxOutput, Temperature: p.Temperature, Tools: p.builtins}
	for _, t := range path {
		req.Turns = append(req.Turns, t.Turn)
	}
	req.Turns = append(req.Turns, sent)
	tools := m.toolsIn(e.ChatKey())
	if picks != nil {
		tools[stickerTool] = picks.tool()
	}

	// The reply is at most two messages, each a stream of its own: "reply",
	// and "reply-2" for what follows words said before tools. The second is
	// opened only once it has something to show, so no empty message is
	// sent or recorded.
	var refs []otogi.EventRef
	stream, said := inv.Stream(ctx, "reply", e.Destination()), false
	update := func(msg otogi.Message) error {
		if len(msg.Parts) == 0 {
			return nil
		}
		if stream == nil {
			stream = inv.Stream(ctx, "reply-2", e.Destination())
		}
		return stream.Update(ctx, present(p, e, msg))
	}
	say := func(msg otogi.Message) error {
		sent, err := stream.Close(ctx, present(p, e, msg))
		refs, stream, said = append(refs, sent...), nil, true
		return err
	}
	out, runErr := llm.Run(llm.ContextWithAgent(ctx, p.Name), p.provider, req, tools, e, llm.Limits{Rounds: p.MaxToolRounds, Reserve: *m.cfg.ToolReserve},
		update, say, func(c llm.CallRecord) { m.recordCall(ctx, e, p, c) })

	// The reply is finished even when ctx ended, so the chat is not left
	// with a partial answer.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	// A picked sticker follows the reply, unless the reply failed.
	var sticker *sentSticker
	if picks != nil && picks.picked != nil {
		sticker = &sentSticker{ID: picks.picked.ID, Text: picks.picked.Text, Listed: picks.listed}
	}
	sends := sticker != nil && runErr == nil
	var closeErr error
	if final := ending(out, said || sends, runErr); stream != nil || len(final.Parts) > 0 {
		if stream == nil {
			stream = inv.Stream(cleanup, "reply-2", e.Destination())
		}
		// A reply that is only a sticker closes its stream empty, which
		// shows nothing: not even the agent's label.
		shown := otogi.Message{}
		if len(final.Parts) > 0 {
			shown = present(p, e, final)
		}
		var last []otogi.EventRef
		last, closeErr = stream.Close(cleanup, shown)
		refs = append(refs, last...)
	}
	messages := make([]sentMessage, 0, len(refs)+1)
	for _, ref := range refs {
		messages = append(messages, sentMessage{Ref: ref})
	}
	var stickerErr error
	if sends {
		var ref otogi.EventRef
		ref, stickerErr = m.sendSticker(cleanup, e, inv, *picks.picked)
		if stickerErr == nil {
			sticker.Ref = string(ref.SourceEvent)
			refs = append(refs, ref)
			messages = append(messages, sentMessage{Ref: ref, Sticker: sticker.Text})
		}
	}
	var ids []int64
	var storeErr error
	if runErr == nil {
		turns := []storedTurn{{Turn: user, SourceID: string(e.SourceID)}}
		for _, t := range out.Turns {
			turns = append(turns, storedTurn{Turn: t})
		}
		ids, storeErr = m.store.thread(cleanup, p.Name, e.Destination(), parent, turns, messages, m.now())
	}
	err = errors.Join(runErr, closeErr, stickerErr, storeErr)
	m.record(cleanup, e, p, trigger, j, parent, ids, out, refs, sticker, err, m.now().Sub(started))
	return err
}

// thread is the path to parent, at most maxTurns turns, starting with a
// user turn.
func (m *Module) thread(ctx context.Context, parent int64, maxTurns int) ([]storedTurn, error) {
	if parent == 0 {
		return nil, nil
	}
	path, err := m.store.path(ctx, parent)
	if err != nil {
		return nil, err
	}
	if len(path) > maxTurns {
		path = path[len(path)-maxTurns:]
	}
	for len(path) > 0 && path[0].Turn.Role != llm.RoleUser {
		path = path[1:]
	}
	return path, nil
}

// userContent is the message as the model gets it: in groups after its
// sender's name, with media inline, or described for an agent with
// media.as_text (the quoted message's by its kept descriptions, without
// waiting), and, when quote is set, after the message it replies to (a reply
// continuing a thread quotes the agent's own turn, which the thread has).
// named is the people it names, in order, and its sender, named or not.
func (m *Module) userContent(ctx context.Context, e otogi.Event, inv otogi.Invocation, p *profile, quote bool, until time.Time) (msg otogi.Message, named []sender) {
	asText := p.Media != nil && p.Media.AsText
	shown := func(media otogi.Media) otogi.Message {
		if asText {
			return m.describedPart(ctx, e.Destination(), media, until)
		}
		return m.mediaPart(ctx, inv, e.Destination(), media)
	}
	if target, ok := replyTarget(e.Message); ok && quote {
		if quoted, found, err := m.store.message(ctx, e.Destination(), string(target.SourceEvent)); err == nil && found && (quoted.Text != "" || len(quoted.Media) > 0) {
			who := quoted.ActorName
			if !quoted.Outgoing {
				who = m.nameOf(e.Platform, quoted.ActorID, quoted.ActorName)
				named = append(named, sender{quoted.ActorID, quoted.ActorName})
			}
			if quoted.Sticker != "" { // an agent's sticker reads as what it shows
				quoted.Text, quoted.Media = m.lineText(ctx, e.Destination(), quoted, false), nil
			}
			msg = llm.Join(msg, otogi.TextMessage("Replying to "+who+":\n"+quoted.Text))
			for _, media := range quoted.Media {
				if asText {
					msg = llm.Join(msg, m.keptPart(ctx, e.Destination(), media))
				} else {
					msg = llm.Join(msg, m.mediaPart(ctx, inv, e.Destination(), media))
				}
			}
		}
	}
	named = append(named, sender{string(e.Actor.ID), speaker(e.Actor)})
	prefix := ""
	if e.Conversation.Kind != otogi.ConversationDirect {
		prefix = m.nameOf(e.Platform, string(e.Actor.ID), speaker(e.Actor)) + ": "
	}
	for i, part := range e.Message.Parts {
		switch {
		case part.Kind == otogi.PartText:
			body, annotations := part.Text, part.Annotations
			if i == 0 {
				body, annotations = withoutLeadingMention(e, body, annotations)
			}
			text := otogi.RichText{}
			text.Write(prefix)
			text.Append(body, annotations)
			prefix = ""
			msg = llm.Join(msg, text.Message())
		case part.Media != nil:
			msg = llm.Join(msg, shown(*part.Media))
		default:
			msg = llm.Join(msg, otogi.TextMessage(describe(part)))
		}
	}
	if prefix != "" { // no text: name the sender before the media
		msg = llm.Join(otogi.TextMessage(strings.TrimSpace(prefix)), msg)
	}
	return msg, named
}

// maxMedia is the largest media part shown to the model, in bytes.
func (m *Module) maxMedia() int64 { return int64(m.cfg.MaxMediaMB) << 20 }

// mediaPart fetches media for the model, or describes it when it is too
// large or cannot be fetched.
func (m *Module) mediaPart(ctx context.Context, inv otogi.Invocation, from otogi.Destination, media otogi.Media) otogi.Message {
	word := mediaWord(media)
	skipped := func(reason string, err error, size int64) {
		m.host.Logger().Warn("chat: media left out", "reason", reason, "media", word, "bytes", size, "error", err)
		payload := map[string]any{"reason": reason, "from": from, "source": media.Source.Kind, "url": media.Source.URL, "locator": media.Source.Locator, "bytes": size, "limit": m.maxMedia()}
		if err != nil {
			payload["error"] = err.Error()
		}
		m.host.Record(ctx, otogi.Record{Kind: "chat.media_skipped", Outcome: "skipped", Payload: payload})
	}
	if media.ByteSize > m.maxMedia() {
		skipped("too large", nil, media.ByteSize)
		return otogi.TextMessage("[" + word + ", too large to show]")
	}
	data, err := media.Source.Inline, error(nil)
	if media.Source.Kind != otogi.MediaSourceInline {
		data, err = inv.FetchMedia(ctx, from, media.Source)
	}
	if err != nil || int64(len(data)) > m.maxMedia() {
		skipped("could not be loaded", err, int64(len(data)))
		return otogi.TextMessage("[" + word + ", could not be loaded]")
	}
	media.Source = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}
	media.ByteSize = int64(len(data))
	return otogi.Message{Roots: []otogi.PartID{"m"}, Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &media}}}
}

// contextText is the chat's recent messages, leaving out those already in
// the thread, and the people who sent them, oldest first.
func (m *Module) contextText(ctx context.Context, e otogi.Event, p *profile, path []storedTurn) (string, []sender, error) {
	inThread := map[string]bool{string(e.SourceID): true}
	for _, t := range path {
		inThread[t.SourceID] = true
		for _, s := range t.Sent {
			inThread[s] = true
		}
	}
	before := e.OccurredAt
	if before.IsZero() {
		before = m.now()
	}
	lines, err := m.store.recent(ctx, e.Destination(), before.Add(time.Millisecond), p.context+len(inThread))
	if err != nil {
		return "", nil, err
	}
	lines = slices.DeleteFunc(lines, func(l line) bool { return inThread[l.SourceID] })
	if len(lines) > p.context {
		lines = lines[len(lines)-p.context:]
	}
	if len(lines) == 0 {
		return "", nil, nil
	}
	var kept []string
	var named []sender
	for _, l := range lines {
		name := l.ActorName + " (this account)"
		if !l.Outgoing {
			name = m.nameOf(e.Platform, l.ActorID, l.ActorName)
			named = append(named, sender{l.ActorID, l.ActorName})
		}
		text := m.lineText(ctx, e.Destination(), l, p.Media != nil)
		kept = append(kept, fmt.Sprintf("[%s] %s: %s", l.At.In(time.Local).Format("15:04"), name, text))
	}
	return "Recent messages in this chat, oldest first, for context:\n" + strings.Join(kept, "\n") + "\n\nThe message to answer:", named, nil
}

// system is the profile's prompt and what the agent needs to know about
// where it is.
func (m *Module) system(p *profile, e otogi.Event) string {
	where := "a private chat"
	switch e.Conversation.Kind {
	case otogi.ConversationGroup:
		where = "a group chat"
	case otogi.ConversationChannel:
		where = "a channel"
	}
	if e.Conversation.Title != "" {
		where += " called " + e.Conversation.Title
	}
	if e.Conversation.Members > 0 {
		where += fmt.Sprintf(" (%d members)", e.Conversation.Members)
	}
	about := ""
	if d := strings.TrimSpace(e.Conversation.Description); d != "" {
		about = " The chat describes itself as: " + d
	}
	return strings.TrimSpace(p.System) + "\n\n" + fmt.Sprintf(
		"You are %s, talking in %s on %s. It is %s. People address you by starting a message with your name, by mentioning you, or by replying to you. "+
			"In group chats each message starts with its sender's name. You speak through a shared account: chat lines marked (this account) were sent by you or by another agent or feature of this bot. "+
			"Write in Markdown.%s",
		p.Name, where, e.Platform, m.now().In(time.Local).Format("Monday, 2 January 2006, 15:04 MST"), about)
}

// present is a reply as the chat shows it: after the profile's label, as a
// reply to the message it answers.
func present(p *profile, e otogi.Event, msg otogi.Message) otogi.Message {
	if p.Label != "" {
		label := otogi.TextMessage(p.Label)
		label.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: len(p.Label), Kind: otogi.AnnotationBold}}
		msg = joinLine(label, msg)
	}
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	return msg
}

// joinLine is llm.Join with a line break instead of a blank line.
func joinLine(a, b otogi.Message) otogi.Message {
	out := llm.Join(a, b)
	if len(b.Parts) > 0 && b.Parts[0].Kind == otogi.PartText && len(out.Parts) > 0 {
		first := &out.Parts[0]
		at := len(a.Parts[len(a.Parts)-1].Text)
		first.Text = first.Text[:at] + "\n" + first.Text[at+2:]
		for i := range first.Annotations {
			if first.Annotations[i].Offset > at {
				first.Annotations[i].Offset--
			}
		}
	}
	return out
}

// ending is the reply's last message: what the model showed after its first
// words before tools went out, saying once how the reply ended when that was
// not a plain answer. said is whether the reply shows something else: those
// words, or a sticker after it; then an empty ending is no message at all.
func ending(out llm.RunResult, said bool, err error) otogi.Message {
	reply := out.Last
	empty := len(reply.Parts) == 0
	silent := empty && !said // the reply showed nothing at all
	note := func(text string) otogi.Message { return llm.Join(reply, otogi.TextMessage("("+text+")")) }
	switch {
	case err != nil && silent:
		return otogi.TextMessage("Sorry, I couldn't reply: " + friendly(err) + ".")
	case err != nil:
		return note("Cut short: " + friendly(err))
	case out.Final.Finish == llm.FinishBlocked && silent:
		return otogi.TextMessage("I can't answer that (" + out.Final.Reason + ").")
	case out.Final.Finish == llm.FinishBlocked:
		return note("The rest was blocked: " + out.Final.Reason)
	case out.Final.Finish == llm.FinishMaxTokens:
		reply = llm.Join(reply, otogi.Message{})
		if n := len(reply.Parts); n > 0 && reply.Parts[n-1].Kind == otogi.PartText {
			reply.Parts[n-1].Text += "…"
		}
	case out.Final.Finish == llm.FinishOther && silent:
		return otogi.TextMessage("I couldn't come up with an answer (" + out.Final.Reason + ").")
	case silent:
		return otogi.TextMessage("I have nothing to add.")
	}
	return withSources(reply, out.Sources)
}

// withSources ends a grounded reply with links to the pages it cites.
func withSources(reply otogi.Message, sources []llm.Source) otogi.Message {
	if len(sources) == 0 {
		return reply
	}
	list := &otogi.RichText{}
	list.Write("Sources: ")
	for i, s := range sources {
		if i > 0 {
			list.Write(", ")
		}
		title := s.Title
		if title == "" {
			title = s.URL
		}
		list.Mark(otogi.AnnotationLink, s.URL, func() { list.Write(title) })
	}
	return llm.Join(reply, list.Message())
}

// friendly says what went wrong in a few words; the event log has the rest.
func friendly(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "it took too long"
	case errors.Is(err, otogi.ErrUnavailable):
		return "the model is busy or unreachable, try again in a minute"
	case errors.Is(err, otogi.ErrUnsupported):
		return "I can't take this kind of message"
	case errors.Is(err, otogi.ErrInvalid):
		return "the model rejected the request"
	}
	return "something went wrong"
}

// recordCall writes a function call's record.
func (m *Module) recordCall(ctx context.Context, e otogi.Event, p *profile, c llm.CallRecord) {
	payload := map[string]any{"profile": p.Name, "function": c.Call.Name, "call": c.Call.ID, "args": c.Call.Args, "result": c.Result.Output}
	outcome := "ok"
	if c.Result.Error != "" {
		outcome, payload["error"] = "error", c.Result.Error
	}
	if c.Panic != "" {
		payload["stack"] = c.Panic
	}
	m.host.Record(ctx, otogi.Record{Kind: "chat.tool", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Outcome: outcome, Duration: c.Duration, Payload: payload})
}

// record writes the reply's record.
func (m *Module) record(ctx context.Context, e otogi.Event, p *profile, trigger string, j *judgment, parent int64, ids []int64, out llm.RunResult, refs []otogi.EventRef, sticker *sentSticker, err error, d time.Duration) {
	rounds := 0
	for _, t := range out.Turns {
		if t.Role == llm.RoleModel {
			rounds++
		}
	}
	payload := map[string]any{
		"profile": p.Name, "provider": p.Provider, "model": p.Model, "trigger": trigger, "parent": parent, "turns": ids, "rounds": rounds,
		"finish": out.Final.Finish, "reason": out.Final.Reason, "wrapped_up": out.WrappedUp, "usage": out.Usage, "sources": out.Sources, "messages": refs,
	}
	if j != nil {
		payload["judged"] = j
	}
	if sticker != nil {
		payload["sticker"] = sticker
	}
	outcome := "ok"
	if err != nil {
		outcome, payload["error"] = "error", err.Error()
	}
	m.host.Record(ctx, otogi.Record{Kind: "chat.reply", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Outcome: outcome, Duration: d, Payload: payload})
}

// withoutLeadingMention drops the @ that addressed the account from the
// start of a message: the agent was addressed, and the mention says nothing
// more.
func withoutLeadingMention(e otogi.Event, text string, annotations []otogi.Annotation) (string, []otogi.Annotation) {
	if !e.Mentioned {
		return text, annotations
	}
	for _, a := range annotations {
		if a.Kind == otogi.AnnotationMention && a.Offset == 0 {
			var r otogi.RichText
			r.Append(text, annotations)
			from := a.Length + len(text[a.Length:]) - len(strings.TrimLeft(text[a.Length:], " "))
			rest := r.Slice(from, r.Len())
			return rest.String(), rest.Annotations()
		}
	}
	return text, annotations
}
