package domain

import (
	"errors"
	"strings"
)

var (
	ErrInvalidTask         = errors.New("invalid task")
	ErrInvalidSession      = errors.New("invalid session")
	ErrInvalidAgentEvent   = errors.New("invalid agent event")
	ErrInvalidTransition   = errors.New("invalid session state transition")
	ErrSessionNotFound     = errors.New("session not found")
	ErrMemoryNotFound      = errors.New("memory not found")
	ErrUnsupportedProvider = errors.New("unsupported provider")
)

// ErrorInfo is safe, provider-neutral failure information stored with a session.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e ErrorInfo) validate() error {
	if strings.TrimSpace(e.Code) == "" || strings.TrimSpace(e.Message) == "" {
		return errors.New("error code and message are required")
	}
	return nil
}
