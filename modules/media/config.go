package media

import (
	"cmp"
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings.
type Config struct {
	Steps    []Step          `json:"steps" minItems:"1" ordered:"true" doc:"The models that describe media, tried in order: when one fails, is blocked, answers out of format, or did not get the media, the next is tried. Configure at least two: Gemini's free tier often answers 503 when it is busy."`
	Language string          `json:"language,omitempty" default:"English" minLength:"1" doc:"The language descriptions are written in, for example Chinese. Text on images is copied as it is."`
	Pictures *PicturesConfig `json:"pictures,omitempty" doc:"Keep the pictures and animations described, in the module's database and so in its backups, for agents to look at one again (chat's media.examine) after the platform's link to it expired, as QQ's do in about an hour. Unset keeps none: agents fetch a picture again, as they do videos, which are never kept."`
}

// PicturesConfig has the module keep the pictures it describes.
type PicturesConfig struct {
	MaxMB int `json:"max_mb,omitempty" minimum:"1" default:"512" doc:"The most the pictures kept may take in all, in MiB: pruned hourly back under it, the longest unseen first. A picture also goes with its description, 30 days after it was last seen. Eku's groups sent 60 to 86 MB of pictures a day, so 512 keeps six to eight days of them."`
}

// defaultPicturesMB is the pictures' limit a pictures block leaves unset.
const defaultPicturesMB = 512

// keepBytes is the most the pictures kept may take, 0 when none are kept.
func (c Config) keepBytes() int64 {
	if c.Pictures == nil {
		return 0
	}
	return int64(cmp.Or(c.Pictures.MaxMB, defaultPicturesMB)) << 20
}

// Step is one model that describes media.
type Step struct {
	Provider string `json:"provider" minLength:"1" format:"llm-provider" doc:"A provider set in the panel under Language models, such as gemini. Use only models that see images and video: one that drops them silently, as deepseek-v4-pro does images, makes descriptions up."`
	Model    string `json:"model" minLength:"1" format:"llm-model" doc:"The provider's model ID, for example gemini-3.5-flash-lite."`
	Thinking string `json:"thinking,omitempty" enum:"minimal,low,medium,high" format:"llm-thinking" doc:"The thinking level; empty is the model's default. minimal is enough to describe media."`
}

// defaultLanguage is the language of descriptions when none is set.
const defaultLanguage = "English"

// step is a Step with its provider resolved.
type step struct {
	Step
	provider llm.Provider
}

// steps resolves each step's provider; an unknown provider fails, so the
// settings are refused.
func (c Config) steps(providers func(string) (llm.Provider, error)) ([]step, error) {
	if len(c.Steps) == 0 {
		return nil, &otogi.SettingError{Path: "steps", Err: errors.New("none configured")}
	}
	out := make([]step, len(c.Steps))
	var errs []error
	for i, s := range c.Steps {
		p, err := providers(s.Provider)
		if err != nil {
			errs = append(errs, &otogi.SettingError{Path: fmt.Sprintf("steps[%d].provider", i), Err: err})
		}
		out[i] = step{Step: s, provider: p}
	}
	return out, errors.Join(errs...)
}
