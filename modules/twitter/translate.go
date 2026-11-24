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

The input is a <post>, with the <quote> it quotes when it quotes one, the <parent> it replies to when it is a reply, and the <parent_quote> that the parent quotes. A post of only pictures has no <post>. Reply in exactly this form and nothing else:
<language>the post's language, named in Simplified Chinese, such as 英语 or 日语</language>
<post>the post in natural Simplified Chinese</post>
<quote>the quote in natural Simplified Chinese</quote>
<parent>the parent in natural Simplified Chinese</parent>
<parent_quote>the parent's quote in natural Simplified Chinese</parent_quote>

Give <language> when the input has a <post>, and each of the others only when the input has that part and it is not already written in Chinese, Simplified or Traditional: a part in Chinese is left out. When the post is in Chinese, <language> is 中文.

Keep URLs, @mentions, #hashtags, emoji, and line breaks exactly as they are. Add no notes or explanations.`

var tagged = map[string]*regexp.Regexp{
	"language": regexp.MustCompile(`(?s)<language>(.*?)</language>`),
	"post":     regexp.MustCompile(`(?s)<post>(.*?)</post>`),
	"quote":    regexp.MustCompile(`(?s)<quote>(.*?)</quote>`),
	"parent":   regexp.MustCompile(`(?s)<parent>(.*?)</parent>`),
	"pquote":   regexp.MustCompile(`(?s)<parent_quote>(.*?)</parent_quote>`),
}

// translate returns what of the post, its quote, the post it replies to,
// and that post's quote is not in Chinese, in Chinese: false when all of it
// is in Chinese already, or none of them has text. The model names the
// language of a post with text; one of only pictures has none to name.
func (tr *translator) translate(ctx context.Context, t tweet) (translation, bool, error) {
	var parts []string
	part := func(tag string, of *tweet) {
		if of != nil && of.Text != "" {
			parts = append(parts, "<"+tag+">"+of.Text+"</"+tag+">")
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
	find := func(tag string) string {
		if m := tagged[tag].FindStringSubmatch(resp.Markdown); m != nil {
			return strings.TrimSpace(m[1])
		}
		return ""
	}
	out := translation{Language: find("language"), Post: find("post"), Quote: find("quote"), Parent: find("parent"), ParentQuote: find("pquote")}
	switch {
	case t.Text == "":
		out.Language, out.Post = "", ""
	case out.Language == "中文":
		out.Post = "" // a post in Chinese has no translation, whatever the model sent
	case out.Language == "" || out.Post == "":
		return translation{}, false, fmt.Errorf("twitter: translate: the model replied out of form: %q", resp.Markdown)
	}
	return out, out != translation{Language: out.Language}, nil
}
