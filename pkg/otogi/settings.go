package otogi

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A module's settings are its Config type, decoded from JSON: field names
// from json tags, and documentation for the operator from doc tags (see
// docs/extensions.md). Duration and Secret are the setting types the
// standard library lacks.

// Duration is a time.Duration that settings write as text, such as "90s",
// "5m", or "720h".
type Duration time.Duration

// MarshalText writes d as time.Duration does, without zero trailing units:
// "5m", not "5m0s".
func (d Duration) MarshalText() ([]byte, error) {
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return []byte(s), nil
}

// UnmarshalText reads a duration as time.ParseDuration does.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("duration: %w: %w", ErrInvalid, err)
	}
	*d = Duration(v)
	return nil
}

// Secret is a setting that must stay out of settings, such as a token.
// Settings hold only a reference to it, ${NAME}; Resolve reads it when the
// module is built.
type Secret string

// SecretReference matches what a Secret holds: ${NAME}.
var SecretReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// UnmarshalText accepts only a reference, never the secret itself.
func (s *Secret) UnmarshalText(text []byte) error {
	if !SecretReference.Match(text) {
		return fmt.Errorf("secret: want a reference to a secret, ${NAME}: %w", ErrInvalid)
	}
	*s = Secret(text)
	return nil
}

// Resolve returns the secret s refers to, as lookup holds it under the
// reference's name. The composition root supplies lookup; a name it does
// not hold is an error wrapping ErrInvalid that names the secret.
func (s Secret) Resolve(lookup func(name string) (string, bool)) (string, error) {
	m := SecretReference.FindStringSubmatch(string(s))
	if m == nil {
		return "", fmt.Errorf("secret: want a reference to a secret, ${NAME}: %w", ErrInvalid)
	}
	v, ok := lookup(m[1])
	if !ok {
		return "", fmt.Errorf("secret %s is not set: %w", m[1], ErrInvalid)
	}
	return v, nil
}
