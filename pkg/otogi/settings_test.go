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
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.fails) {
			t.Errorf("%s: %v, want invalid naming %s", c.secret, err, c.fails)
		}
	}
}
