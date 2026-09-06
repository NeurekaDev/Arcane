package oidcjwk

import "regexp"

var errorURLPattern = regexp.MustCompile(`https?://[^\s"<>]+`)

// Cache errors include endpoint and redirect URLs, which may contain credentials.
type initializationError struct {
	cause error
}

func (e *initializationError) Error() string {
	return "failed to initialize JWKS: " + errorURLPattern.ReplaceAllString(e.cause.Error(), "[redacted URL]")
}

func (e *initializationError) Unwrap() error {
	return e.cause
}
