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
	Provider string `json:"provider" minLength:"1" doc:"A language model provider configured under llm in otogi.yaml, such as gemini."`
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
}

const translatePrompt = `You translate posts from X into Simplified Chinese.

The input is a <post>, and the <quote> it quotes when it quotes one. Reply in exactly this form and nothing else:
<language>the post's language, named in Simplified Chinese, such as 英语 or 日语</language>
<post>the post in natural Simplified Chinese</post>
<quote>the quote in natural Simplified Chinese, only when the input has a quote</quote>

When the post is already written in Chinese, Simplified or Traditional, reply with only: <language>中文</language>

Keep URLs, @mentions, #hashtags, emoji, and line breaks exactly as they are. Add no notes or explanations.`

var tagged = map[string]*regexp.Regexp{
	"language": regexp.MustCompile(`(?s)<language>(.*?)</language>`),
	"post":     regexp.MustCompile(`(?s)<post>(.*?)</post>`),
	"quote":    regexp.MustCompile(`(?s)<quote>(.*?)</quote>`),
}

// translate returns the post in Chinese, or false when it is in Chinese
// already.
func (tr *translator) translate(ctx context.Context, t tweet) (translation, bool, error) {
	input := "<post>" + t.Text + "</post>"
	if t.Quote != nil && t.Quote.Text != "" {
		input += "\n<quote>" + t.Quote.Text + "</quote>"
	}
	resp, err := tr.provider.Generate(ctx, llm.Request{
		Model: tr.cfg.Model, System: translatePrompt, Thinking: tr.cfg.Thinking,
		Turns: []llm.Turn{{Role: llm.RoleUser, Content: otogi.TextMessage(input)}},
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
	out := translation{Language: find("language"), Post: find("post"), Quote: find("quote")}
	switch {
	case out.Language == "中文":
		return translation{}, false, nil
	case out.Language == "" || out.Post == "":
		return translation{}, false, fmt.Errorf("twitter: translate: the model replied out of form: %q", resp.Markdown)
	}
	return out, true, nil
}
