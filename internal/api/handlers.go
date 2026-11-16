package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
)

type Handler struct {
	maxBodyBytes    int64
	coordinator     TaskCoordinator
	sessions        SessionManager
	events          ports.EventPublisher
	execContext     context.Context
	providerCatalog ProviderCatalog
	memories        MemoryManager
	logger          *slog.Logger
}

func newHandler(maxBodyBytes int64, dependencies Dependencies, logger *slog.Logger) *Handler {
	return &Handler{
		maxBodyBytes:    maxBodyBytes,
		coordinator:     dependencies.Coordinator,
		sessions:        dependencies.Sessions,
		events:          dependencies.Events,
		execContext:     dependencies.ExecutionContext,
		providerCatalog: dependencies.Providers,
		memories:        dependencies.Memories,
		logger:          logger,
	}
}

func (h *Handler) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /api/v1/tasks", h.createTask)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}", h.getSession)
	mux.HandleFunc("GET /api/v1/sessions", h.listSessions)
	mux.HandleFunc("GET /api/v1/providers", h.listProviders)
	mux.HandleFunc("POST /api/v1/memories", h.createMemory)
	mux.HandleFunc("GET /api/v1/memories/{memory_id}", h.getMemory)
	mux.HandleFunc("POST /api/v1/memories/search", h.searchMemories)
	mux.HandleFunc("PATCH /api/v1/memories/{memory_id}", h.updateMemory)
	mux.HandleFunc("POST /api/v1/memories/{memory_id}/archive", h.archiveMemory)
	mux.HandleFunc("DELETE /api/v1/memories/{memory_id}", h.deleteMemory)
	mux.HandleFunc("POST /api/v1/sessions/{session_id}/cancel", h.cancelSession)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}/events", h.streamEvents)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "The requested endpoint was not found.")
	})
	return mux
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "ok"})
}

type createTaskRequest struct {
	Task taskInput `json:"task"`
}

type taskInput struct {
	ID               domain.TaskID        `json:"id"`
	Description      string               `json:"description"`
	Provider         domain.AgentProvider `json:"provider"`
	WorkingDirectory string               `json:"working_directory"`
}

type taskResponse struct {
	Accepted bool           `json:"accepted"`
	Session  domain.Session `json:"session"`
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var request createTaskRequest
	if err := h.decodeJSON(w, r, &request); err != nil {
		writeMappedError(w, err)
		return
	}
	if err := r.Context().Err(); err != nil {
		return
	}
	if err := h.execContext.Err(); err != nil {
		writeMappedError(w, err)
		return
	}
	task := domain.Task{
		ID: request.Task.ID, Description: request.Task.Description,
		Provider: request.Task.Provider, WorkingDirectory: request.Task.WorkingDirectory,
		CreatedAt: time.Now().UTC(),
	}
	// Provider processes outlive the HTTP request. The application execution
	// context owns accepted work; request cancellation is checked before dispatch.
	session, err := h.coordinator.Execute(h.execContext, task)
	if err != nil {
		if session.ID != "" && session.Status == domain.SessionFailed && !errors.Is(err, providers.ErrProviderNotFound) {
			writeJSON(w, http.StatusBadGateway, errorEnvelope{Error: apiError{
				Code: "provider_start_failed", Message: "The provider failed to start the task.", SessionID: string(session.ID),
			}})
			return
		}
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, taskResponse{Accepted: true, Session: session})
}

type sessionResponse struct {
	Session domain.Session `json:"session"`
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	lister, ok := h.sessions.(interface {
		List(context.Context) ([]domain.Session, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "session_listing_unavailable", "Session listing is not available.")
		return
	}
	items, err := lister.List(r.Context())
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Sessions []domain.Session `json:"sessions"`
	}{Sessions: items})
}

func (h *Handler) listProviders(w http.ResponseWriter, _ *http.Request) {
	type providerItem struct {
		Name      domain.AgentProvider `json:"name"`
		Available bool                 `json:"available"`
	}
	items := make([]providerItem, 0, len(domain.SupportedProviders()))
	for _, name := range domain.SupportedProviders() {
		available := h.providerCatalog != nil && h.providerCatalog.Has(name)
		items = append(items, providerItem{Name: name, Available: available})
	}
	writeJSON(w, http.StatusOK, struct {
		Providers []providerItem `json:"providers"`
	}{Providers: items})
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.sessions.Get(r.Context(), domain.SessionID(r.PathValue("session_id")))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{Session: session})
}

type cancelResponse struct {
	Accepted        bool           `json:"accepted"`
	AlreadyTerminal bool           `json:"already_terminal"`
	Session         domain.Session `json:"session"`
}

func (h *Handler) cancelSession(w http.ResponseWriter, r *http.Request) {
	id := domain.SessionID(r.PathValue("session_id"))
	current, err := h.sessions.Get(r.Context(), id)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if current.Status.Terminal() {
		writeJSON(w, http.StatusOK, cancelResponse{AlreadyTerminal: true, Session: current})
		return
	}
	updated, err := h.sessions.Interrupt(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidTransition) {
			latest, getErr := h.sessions.Get(r.Context(), id)
			if getErr == nil && latest.Status.Terminal() {
				writeJSON(w, http.StatusOK, cancelResponse{AlreadyTerminal: true, Session: latest})
				return
			}
		}
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, cancelResponse{Accepted: true, Session: updated})
}

func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := domain.SessionID(r.PathValue("session_id"))
	if _, err := h.sessions.Get(r.Context(), sessionID); err != nil {
		writeMappedError(w, err)
		return
	}
	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var (
		eventCh  <-chan domain.AgentEvent
		detailed ports.EventSubscription
		err      error
	)
	if publisher, ok := h.events.(ports.DetailedEventPublisher); ok {
		detailed, err = publisher.SubscribeStream(streamCtx, sessionID)
		if err == nil {
			eventCh = detailed.Events()
			defer detailed.Cancel()
		}
	} else {
		eventCh, err = h.events.Subscribe(streamCtx, sessionID)
	}
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if eventCh == nil {
		writeError(w, http.StatusServiceUnavailable, "event_stream_unavailable", "The event stream is unavailable.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "The HTTP server cannot stream events.")
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-eventCh:
			if !open {
				if detailed != nil {
					if reason := detailed.Err(); reason != nil && !errors.Is(reason, context.Canceled) {
						h.logger.Warn("SSE event subscription ended", "session_id", sessionID, "reason", reason)
					}
				} else {
					h.logger.Warn("SSE event subscription closed without a termination reason", "session_id", sessionID)
				}
				return
			}
			if err := writeSSE(w, flusher, event); err != nil {
				h.logger.Debug("SSE client disconnected", "session_id", sessionID, "error", err)
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event domain.AgentEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode agent event: %w", err)
	}
	if event.ID != "" && !strings.ContainsAny(event.ID, "\r\n") {
		if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
			return err
		}
	}
	if event.Type != "" && !strings.ContainsAny(string(event.Type), "\r\n") {
		if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (h *Handler) decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("%w: Content-Type must be application/json", ErrInvalidRequest)
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: %v", ErrRequestTooLarge, err)
		}
		return fmt.Errorf("%w: invalid JSON body: %v", ErrInvalidRequest, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body must contain one JSON value", ErrInvalidRequest)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
