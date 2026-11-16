package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
	"github.com/Imkumar80/agent-orchestrator-/internal/session"
)

var (
	ErrInvalidDependency = errors.New("HTTP API dependency is nil")
	ErrInvalidConfig     = errors.New("invalid HTTP API configuration")
	ErrInvalidRequest    = errors.New("invalid HTTP request")
	ErrRequestTooLarge   = errors.New("HTTP request body is too large")
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func writeMappedError(w http.ResponseWriter, err error) {
	status, code, message := mapError(err)
	writeError(w, status, code, message)
}

func mapError(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrRequestTooLarge):
		return http.StatusRequestEntityTooLarge, "request_too_large", "The request body exceeds the allowed size."
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, domain.ErrInvalidTask):
		return http.StatusBadRequest, "invalid_request", "The request is invalid."
	case errors.Is(err, domain.ErrUnsupportedProvider):
		return http.StatusUnprocessableEntity, "unsupported_provider", "The requested provider is not supported."
	case errors.Is(err, domain.ErrSessionNotFound):
		return http.StatusNotFound, "session_not_found", "The session was not found."
	case errors.Is(err, providers.ErrProviderNotFound):
		return http.StatusServiceUnavailable, "provider_unavailable", "The requested provider is not enabled."
	case errors.Is(err, session.ErrUnsupportedCapability):
		return http.StatusNotImplemented, "unsupported_operation", "The selected provider does not support this operation."
	case errors.Is(err, domain.ErrInvalidTransition):
		return http.StatusConflict, "invalid_session_state", "The requested operation is not valid for the current session state."
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "operation_timeout", "The operation timed out."
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "operation_cancelled", "The operation was cancelled."
	default:
		return http.StatusInternalServerError, "internal_error", "The request could not be completed."
	}
}
