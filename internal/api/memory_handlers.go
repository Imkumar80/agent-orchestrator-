package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/memory"
)

type createMemoryRequest struct {
	Scope        domain.MemoryScope  `json:"scope"`
	ScopeID      string              `json:"scope_id,omitempty"`
	Type         domain.MemoryType   `json:"type"`
	Content      string              `json:"content"`
	Summary      string              `json:"summary,omitempty"`
	Source       domain.MemorySource `json:"source"`
	Confidence   *float64            `json:"confidence,omitempty"`
	Importance   *int                `json:"importance,omitempty"`
	Tags         []string            `json:"tags,omitempty"`
	Metadata     json.RawMessage     `json:"metadata,omitempty"`
	TaskID       *domain.TaskID      `json:"task_id,omitempty"`
	SessionID    *domain.SessionID   `json:"session_id,omitempty"`
	SupersedesID *domain.MemoryID    `json:"supersedes_id,omitempty"`
	ExpiresAt    *time.Time          `json:"expires_at,omitempty"`
}

type memoryEnvelope struct {
	Memory domain.Memory `json:"memory"`
}

func (h *Handler) createMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Memory storage is unavailable.")
		return
	}
	var request createMemoryRequest
	if err := h.decodeJSON(w, r, &request); err != nil {
		writeMappedError(w, err)
		return
	}
	if request.Scope == "" {
		request.Scope = domain.MemoryScopeGlobal
	}
	if request.Type == "" {
		request.Type = domain.MemorySemantic
	}
	if request.Source == "" {
		request.Source = domain.MemorySourceUser
	}
	confidence := 1.0
	if request.Confidence != nil {
		confidence = *request.Confidence
	}
	importance := 3
	if request.Importance != nil {
		importance = *request.Importance
	}
	if request.Scope == domain.MemoryScopeTask && request.TaskID == nil {
		value := domain.TaskID(request.ScopeID)
		request.TaskID = &value
	}
	if request.Scope == domain.MemoryScopeSession && request.SessionID == nil {
		value := domain.SessionID(request.ScopeID)
		request.SessionID = &value
	}
	value, err := h.memories.Store(r.Context(), domain.Memory{Scope: request.Scope, ScopeID: request.ScopeID, Type: request.Type, Content: request.Content, Summary: request.Summary, Source: request.Source, Confidence: confidence, Importance: importance, Tags: request.Tags, Metadata: request.Metadata, TaskID: request.TaskID, SessionID: request.SessionID, SupersedesID: request.SupersedesID, ExpiresAt: request.ExpiresAt})
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, memoryEnvelope{Memory: value})
}

func (h *Handler) getMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Memory storage is unavailable.")
		return
	}
	value, err := h.memories.Get(r.Context(), domain.MemoryID(r.PathValue("memory_id")))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memoryEnvelope{Memory: value})
}

func (h *Handler) searchMemories(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Memory storage is unavailable.")
		return
	}
	var query domain.MemorySearchQuery
	if err := h.decodeJSON(w, r, &query); err != nil {
		writeMappedError(w, err)
		return
	}
	results, err := h.memories.Search(r.Context(), query)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Results []memory.Result `json:"results"`
	}{Results: results})
}

type updateMemoryRequest struct {
	Scope      *domain.MemoryScope  `json:"scope,omitempty"`
	ScopeID    *string              `json:"scope_id,omitempty"`
	Type       *domain.MemoryType   `json:"type,omitempty"`
	Content    *string              `json:"content,omitempty"`
	Summary    *string              `json:"summary,omitempty"`
	Source     *domain.MemorySource `json:"source,omitempty"`
	Confidence *float64             `json:"confidence,omitempty"`
	Importance *int                 `json:"importance,omitempty"`
	Tags       *[]string            `json:"tags,omitempty"`
	Metadata   *json.RawMessage     `json:"metadata,omitempty"`
	TaskID     *domain.TaskID       `json:"task_id,omitempty"`
	SessionID  *domain.SessionID    `json:"session_id,omitempty"`
	ExpiresAt  *time.Time           `json:"expires_at,omitempty"`
}

func (h *Handler) updateMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Memory storage is unavailable.")
		return
	}
	var patch updateMemoryRequest
	if err := h.decodeJSON(w, r, &patch); err != nil {
		writeMappedError(w, err)
		return
	}
	id := domain.MemoryID(r.PathValue("memory_id"))
	value, err := h.memories.Get(r.Context(), id)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if patch.Scope != nil {
		value.Scope = *patch.Scope
	}
	if patch.ScopeID != nil {
		value.ScopeID = *patch.ScopeID
	}
	if patch.Type != nil {
		value.Type = *patch.Type
	}
	if patch.Content != nil {
		value.Content = *patch.Content
	}
	if patch.Summary != nil {
		value.Summary = *patch.Summary
	}
	if patch.Source != nil {
		value.Source = *patch.Source
	}
	if patch.Confidence != nil {
		value.Confidence = *patch.Confidence
	}
	if patch.Importance != nil {
		value.Importance = *patch.Importance
	}
	if patch.Tags != nil {
		value.Tags = *patch.Tags
	}
	if patch.Metadata != nil {
		value.Metadata = *patch.Metadata
	}
	if patch.TaskID != nil {
		value.TaskID = patch.TaskID
	}
	if patch.SessionID != nil {
		value.SessionID = patch.SessionID
	}
	if patch.ExpiresAt != nil {
		value.ExpiresAt = patch.ExpiresAt
	}
	updated, err := h.memories.Update(r.Context(), value)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memoryEnvelope{Memory: updated})
}

func (h *Handler) archiveMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Memory storage is unavailable.")
		return
	}
	value, err := h.memories.Archive(r.Context(), domain.MemoryID(r.PathValue("memory_id")))
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memoryEnvelope{Memory: value})
}

func (h *Handler) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Memory storage is unavailable.")
		return
	}
	if err := h.memories.Delete(r.Context(), domain.MemoryID(strings.TrimSpace(r.PathValue("memory_id")))); err != nil {
		writeMappedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
