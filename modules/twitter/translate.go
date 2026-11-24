package twitter

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Translate configures the translated cards.
type Translate struct {
	Provider string `json:"provider" minLength:"1" doc:"A language model provider set in the panel under Language models, such as gemini."`
	Model    string `json:"model" minLength:"1" doc:"The provider's model ID."`
	Thinking string `json:"thinking,omitempty" enum:"minimal,low,medium,high" doc:"How much the model thinks before answering; unset is the model's default."`
}

// translator asks a language model what language a post is in and, unless
// it is Chinese, for the post in Simplified Chinese.
type translator struct {
	cfg      Translate
	provider llm.Provider
}

// translation is a post in Simplified Chinese.
type translation struct {
	Language    string // the original's, named in Chinese: 英语, 日语
	Post, Quote string
	Parent      string // the post replied to
	ParentQuote string // the post that one quotes
}

const translatePrompt = `You translate posts from X into Simplified Chinese.

The input has up to four parts: a <post>, the <quote> it quotes, the <parent> it replies to, and the <parent_quote> that the parent quotes. A post of only pictures has no <post>. The parts are separate texts by different people, and each may be in a different language.

For every part of the input, reply with one element of the same name, in the input's order, and nothing else:
<post language="英语">the part in natural Simplified Chinese</post>

language is the language that part is written in, named in Simplified Chinese, such as 英语 or 日语. Judge each part on its own, whatever language the other parts are in. A part already written in Chinese, Simplified or Traditional, has language="中文" and an empty element: <quote language="中文"></quote>. Every other part is translated into Simplified Chinese: never give a part back in the language it came in.

Keep URLs, @mentions, #hashtags, emoji, and line breaks exactly as they are. Add no notes or explanations.`

// tagged finds a part in the model's reply: its language and its text.
var tagged = map[string]*regexp.Regexp{
	"post":         regexp.MustCompile(`(?s)<post language="([^"]*)">(.*?)</post>`),
	"quote":        regexp.MustCompile(`(?s)<quote language="([^"]*)">(.*?)</quote>`),
	"parent":       regexp.MustCompile(`(?s)<parent language="([^"]*)">(.*?)</parent>`),
	"parent_quote": regexp.MustCompile(`(?s)<parent_quote language="([^"]*)">(.*?)</parent_quote>`),
}

// translate returns what of the post, its quote, the post it replies to,
// and that post's quote is not in Chinese, in Chinese: false when all of it
// is in Chinese already, or none of them has text. The model names the
// language of a post with text; one of only pictures has none to name.
func (tr *translator) translate(ctx context.Context, t tweet) (translation, bool, error) {
	var parts []string
	given := map[string]string{}
	part := func(tag string, of *tweet) {
		if of != nil && of.Text != "" {
			parts = append(parts, "<"+tag+">"+of.Text+"</"+tag+">")
			given[tag] = strings.TrimSpace(of.Text)
		}
	}
	part("post", &t)
	part("quote", t.Quote)
	part("parent", t.Parent)
	if t.Parent != nil {
		part("parent_quote", t.Parent.Quote)
	}
	if len(parts) == 0 {
		return translation{}, false, nil
	}
	resp, err := tr.provider.Generate(ctx, llm.Request{
		Model: tr.cfg.Model, System: translatePrompt, Thinking: tr.cfg.Thinking,
		Turns: []llm.Turn{{Role: llm.RoleUser, Content: otogi.TextMessage(strings.Join(parts, "\n"))}},
	}, nil)
	if err != nil {
		return translation{}, false, fmt.Errorf("twitter: translate: %w", err)
	}
	// find returns a part's language and its translation: none when the
	// part is in Chinese, or the model gave it back as it came, which
	// gemini-3.5-flash-lite did with one part of two in different languages.
	find := func(tag string) (language, text string) {
		m := tagged[tag].FindStringSubmatch(resp.Markdown)
		if m == nil {
			return "", ""
		}
		language, text = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if language == "中文" || text == given[tag] {
			text = ""
		}
		return language, text
	}
	var out translation
	out.Language, out.Post = find("post")
	_, out.Quote = find("quote")
	_, out.Parent = find("parent")
	_, out.ParentQuote = find("parent_quote")
	switch {
	case t.Text == "":
		out.Language, out.Post = "", ""
	case out.Language == "中文":
	case out.Language == "" || out.Post == "":
		return translation{}, false, fmt.Errorf("twitter: translate: the model replied out of form: %q", resp.Markdown)
	}
	return out, out != translation{Language: out.Language}, nil
}
