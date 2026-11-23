package openai

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// request is a streamed Chat Completions request.
type request struct {
	Model           string            `json:"model"`
	Messages        []json.RawMessage `json:"messages"`
	Stream          bool              `json:"stream"`
	StreamOptions   streamOptions     `json:"stream_options"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	Tools           []map[string]any  `json:"tools,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// builtins are the built-in tools the service runs itself, by their wire
// type: DeepSeek's server tools.
var builtins = map[string]string{llm.BuiltinWebSearch: "deepseek_web_search", llm.BuiltinURLContext: "deepseek_read_url"}

// toolList declares the request's tools on the wire.
func toolList(specs []llm.ToolSpec) ([]map[string]any, error) {
	var tools []map[string]any
	for _, s := range specs {
		switch {
		case s.Function != nil:
			fn := map[string]any{"name": s.Function.Name, "description": s.Function.Description}
			if len(s.Function.Parameters) > 0 {
				fn["parameters"] = s.Function.Parameters
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		case builtins[s.Builtin] != "":
			tools = append(tools, map[string]any{"type": builtins[s.Builtin]})
		default:
			return nil, fmt.Errorf("tool %q: %w", s.Builtin, otogi.ErrUnsupported)
		}
	}
	return tools, nil
}

// replacedPart is a media or file part sent as a text mark, for the record.
type replacedPart struct {
	Turn      int    `json:"turn"`
	Part      string `json:"part"`
	MediaType string `json:"media_type"`
	Bytes     int    `json:"bytes"`
}

// messages converts the request's system instruction and turns to Chat
// Completions messages. A model turn this provider made is resent exactly as
// it was received.
func (p *Provider) messages(req llm.Request) ([]json.RawMessage, []replacedPart, error) {
	var out []json.RawMessage
	add := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out = append(out, data)
		return nil
	}
	if req.System != "" {
		if err := add(map[string]any{"role": "system", "content": req.System}); err != nil {
			return nil, nil, fmt.Errorf("openai %s: system: %w", p.name, err)
		}
	}
	var replaced []replacedPart
	for i, t := range req.Turns {
		var err error
		switch {
		case t.Role == llm.RoleModel && t.Native != nil && t.Native.Provider == p.name:
			var msg map[string]json.RawMessage
			if err := json.Unmarshal(t.Native.Data, &msg); err != nil {
				return nil, nil, fmt.Errorf("openai %s: turn %d: native message: %w: %w", p.name, i, otogi.ErrInvalid, err)
			}
			msg["role"] = json.RawMessage(`"assistant"`)
			err = add(msg)
		case t.Role == llm.RoleTool:
			for _, r := range t.Results {
				content := string(r.Output)
				if r.Error != "" {
					data, _ := json.Marshal(map[string]string{"error": r.Error})
					content = string(data)
				}
				if err = add(map[string]any{"role": "tool", "tool_call_id": r.CallID, "content": content}); err != nil {
					break
				}
			}
		default:
			var msg map[string]any
			var gone []replacedPart
			msg, gone, err = turnMessage(t)
			for _, r := range gone {
				r.Turn = i
				replaced = append(replaced, r)
			}
			if err == nil {
				err = add(msg)
			}
		}
		if err != nil {
			return nil, nil, fmt.Errorf("openai %s: turn %d: %w", p.name, i, err)
		}
	}
	return out, replaced, nil
}

// turnMessage converts a user turn, or a model turn with no Native message
// of this provider. Images go as image_url parts of user messages; any other
// media, and images in model turns, which Chat Completions does not take,
// become text marks.
func turnMessage(t llm.Turn) (map[string]any, []replacedPart, error) {
	role := "user"
	if t.Role == llm.RoleModel {
		role = "assistant"
	}
	var parts []map[string]any
	var replaced []replacedPart
	images := false
	for _, part := range t.Content.Parts {
		switch part.Kind {
		case otogi.PartText:
			if text := markdown.Format(part.Text, part.Annotations); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case otogi.PartMedia, otogi.PartFile:
			if part.Media == nil || part.Media.Source.Kind != otogi.MediaSourceInline {
				return nil, nil, fmt.Errorf("media part %q is not inline (see llm.Inline): %w", part.ID, otogi.ErrInvalid)
			}
			data := part.Media.Source.Inline
			mime := part.Media.MediaType
			if mime == "" {
				mime, _, _ = strings.Cut(http.DetectContentType(data), ";")
			}
			if strings.HasPrefix(mime, "image/") && role == "user" {
				images = true
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}})
				continue
			}
			kind, _, _ := strings.Cut(mime, "/")
			if kind != "image" && kind != "video" && kind != "audio" {
				kind = "file"
			}
			mark := "[" + kind + ": not shown, this model takes text and images only]"
			if role == "assistant" {
				mark = "[" + kind + ": not shown]"
			}
			parts = append(parts, map[string]any{"type": "text", "text": mark})
			replaced = append(replaced, replacedPart{Part: string(part.ID), MediaType: mime, Bytes: len(data)})
		default:
			return nil, nil, fmt.Errorf("%s part %q: %w", part.Kind, part.ID, otogi.ErrUnsupported)
		}
	}
	msg := map[string]any{"role": role}
	if images {
		msg["content"] = parts
	} else {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			texts = append(texts, part["text"].(string))
		}
		msg["content"] = strings.Join(texts, "\n\n")
	}
	if len(t.Calls) > 0 {
		calls := make([]map[string]any, 0, len(t.Calls))
		for _, c := range t.Calls {
			args := string(c.Args)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": args}})
		}
		msg["tool_calls"] = calls
	}
	return msg, replaced, nil
}

// digest is v's JSON form with the bytes a conversation resends on every
// call replaced by their size and SHA-256, for records: images in data URLs,
// and the signatures of pages a server tool read, which carry the page.
func digest(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("unencodable: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Sprintf("undecodable: %v", err)
	}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if url, ok := v["url"].(string); ok && strings.HasPrefix(url, "data:") {
				if _, encoded, ok := strings.Cut(url, ";base64,"); ok {
					v["url"] = sizeAndSum(encoded)
				}
			}
			if signature, ok := v["signature"].(string); ok {
				v["signature"] = sizeAndSum(signature)
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(out)
	return out
}

// sizeAndSum is the size and SHA-256 of base64 data, or of the string itself
// when it is not base64.
func sizeAndSum(s string) map[string]any {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		raw = []byte(s)
	}
	sum := sha256.Sum256(raw)
	return map[string]any{"size": len(raw), "sha256": fmt.Sprintf("%x", sum)}
}
