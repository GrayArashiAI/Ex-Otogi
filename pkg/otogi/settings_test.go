package otogi

import (
	"errors"
	"strings"
	"testing"
)

// A secret resolves to what the lookup holds under its name; a name the
// lookup lacks, or text that is no reference, is an invalid setting.
func TestSecretResolve(t *testing.T) {
	lookup := func(name string) (string, bool) {
		v, ok := map[string]string{"TOKEN": "t0p"}[name]
		return v, ok
	}
	for _, c := range []struct {
		secret Secret
		want   string
		fails  string
	}{
		{secret: "${TOKEN}", want: "t0p"},
		{secret: "${OTHER}", fails: "OTHER"},
		{secret: "t0p", fails: "${NAME}"},
	} {
		got, err := c.secret.Resolve(lookup)
		if c.fails == "" {
			if err != nil || got != c.want {
				t.Errorf("%s: %q, %v", c.secret, got, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.fails) || strings.HasSuffix(err.Error(), ErrInvalid.Error()) {
			t.Errorf("%s: %v, want an error naming %s, with no suffix", c.secret, err, c.fails)
		}
	}
}

// A setting refused names its path and says why, with nothing after; it is
// ErrInvalid, alone or joined with others, and wraps its reason.
func TestSettingError(t *testing.T) {
	reason := errors.New("not a time zone")
	err := &SettingError{Path: "profiles[0].timezone", Err: reason}
	if got := err.Error(); got != "profiles[0].timezone: not a time zone" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, reason) {
		t.Errorf("%v is not ErrInvalid and its reason", err)
	}
	joined := errors.Join(err, &SettingError{Path: "chrome", Err: errors.New("no such file")})
	if !errors.Is(joined, ErrInvalid) || joined.Error() != "profiles[0].timezone: not a time zone\nchrome: no such file" {
		t.Errorf("joined: %v", joined)
	}
	if got := (&SettingError{Err: reason}).Error(); got != "not a time zone" {
		t.Errorf("the whole settings' error = %q", got)
	}
}
