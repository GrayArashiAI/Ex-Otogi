package otogi

import "errors"

var (
	// ErrInvalid reports a value that violates a contract, such as an event
	// that fails Validate.
	ErrInvalid = errors.New("invalid")
	// ErrUnavailable reports a component that cannot take work right now,
	// for example a kernel that is not started or is draining.
	ErrUnavailable = errors.New("unavailable")
	// ErrUnsupported reports valid content that a driver cannot represent on
	// its platform.
	ErrUnsupported = errors.New("unsupported")
	// ErrRefused reports content the platform refused for what it shows or
	// says, such as a moderation filter's verdict. The same content is
	// refused again; changed content may pass.
	ErrRefused = errors.New("refused")
)
