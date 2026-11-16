package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type MemoryID string
type MemoryScope string
type MemoryType string
type MemorySource string

const (
	MemoryScopeGlobal  MemoryScope = "global"
	MemoryScopeTask    MemoryScope = "task"
	MemoryScopeSession MemoryScope = "session"

	MemoryWorking  MemoryType = "working"
	MemoryEpisodic MemoryType = "episodic"
	MemorySemantic MemoryType = "semantic"

	MemorySourceUser   MemorySource = "user"
	MemorySourceAgent  MemorySource = "agent"
	MemorySourceTool   MemorySource = "tool"
	MemorySourceSystem MemorySource = "system"
)

var ErrInvalidMemory = fmt.Errorf("invalid memory")

// Memory is provider-neutral knowledge with explicit scope and provenance.
type Memory struct {
	ID           MemoryID        `json:"id"`
	Scope        MemoryScope     `json:"scope"`
	ScopeID      string          `json:"scope_id,omitempty"`
	Type         MemoryType      `json:"type"`
	Content      string          `json:"content"`
	Summary      string          `json:"summary,omitempty"`
	Source       MemorySource    `json:"source"`
	Confidence   float64         `json:"confidence"`
	Importance   int             `json:"importance"`
	Tags         []string        `json:"tags,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	TaskID       *TaskID         `json:"task_id,omitempty"`
	SessionID    *SessionID      `json:"session_id,omitempty"`
	SupersedesID *MemoryID       `json:"supersedes_id,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	ArchivedAt   *time.Time      `json:"archived_at,omitempty"`
}

func (m Memory) Validate() error {
	invalid := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidMemory, reason) }
	if strings.TrimSpace(string(m.ID)) == "" {
		return invalid("ID is required")
	}
	switch m.Scope {
	case MemoryScopeGlobal:
		if m.ScopeID != "" {
			return invalid("global scope must not have a scope ID")
		}
	case MemoryScopeTask:
		if strings.TrimSpace(m.ScopeID) == "" {
			return invalid("task scope ID is required")
		}
		if m.TaskID == nil || string(*m.TaskID) != m.ScopeID {
			return invalid("task scope must match task_id")
		}
	case MemoryScopeSession:
		if strings.TrimSpace(m.ScopeID) == "" {
			return invalid("session scope ID is required")
		}
		if m.SessionID == nil || string(*m.SessionID) != m.ScopeID {
			return invalid("session scope must match session_id")
		}
	default:
		return invalid(fmt.Sprintf("unknown scope %q", m.Scope))
	}
	switch m.Type {
	case MemoryWorking, MemoryEpisodic, MemorySemantic:
	default:
		return invalid(fmt.Sprintf("unknown type %q", m.Type))
	}
	switch m.Source {
	case MemorySourceUser, MemorySourceAgent, MemorySourceTool, MemorySourceSystem:
	default:
		return invalid(fmt.Sprintf("unknown source %q", m.Source))
	}
	if strings.TrimSpace(m.Content) == "" || len(m.Content) > 32768 {
		return invalid("content must contain 1 to 32768 bytes")
	}
	if len(m.Summary) > 4096 {
		return invalid("summary exceeds 4096 bytes")
	}
	if math.IsNaN(m.Confidence) || math.IsInf(m.Confidence, 0) || m.Confidence < 0 || m.Confidence > 1 {
		return invalid("confidence must be between 0 and 1")
	}
	if m.Importance < 1 || m.Importance > 5 {
		return invalid("importance must be between 1 and 5")
	}
	if len(m.Tags) > 32 {
		return invalid("at most 32 tags are allowed")
	}
	seen := make(map[string]struct{}, len(m.Tags))
	for _, tag := range m.Tags {
		clean := strings.TrimSpace(tag)
		if clean == "" || len(clean) > 64 {
			return invalid("tags must contain 1 to 64 non-space bytes")
		}
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			return invalid("tags must be unique")
		}
		seen[key] = struct{}{}
	}
	if len(m.Metadata) > 0 {
		if !json.Valid(m.Metadata) {
			return invalid("metadata must be valid JSON")
		}
		var obj map[string]any
		if err := json.Unmarshal(m.Metadata, &obj); err != nil || obj == nil {
			return invalid("metadata must be a JSON object")
		}
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() || m.UpdatedAt.Before(m.CreatedAt) {
		return invalid("created_at and updated_at must be valid and ordered")
	}
	if m.Type == MemoryWorking && m.ExpiresAt == nil {
		return invalid("working memory requires expires_at")
	}
	if m.ExpiresAt != nil && !m.ExpiresAt.After(m.CreatedAt) {
		return invalid("expires_at must be after created_at")
	}
	if m.ArchivedAt != nil && (m.ArchivedAt.Before(m.CreatedAt) || m.UpdatedAt.Before(*m.ArchivedAt)) {
		return invalid("archived_at must be between created_at and updated_at")
	}
	if m.SupersedesID != nil && (strings.TrimSpace(string(*m.SupersedesID)) == "" || *m.SupersedesID == m.ID) {
		return invalid("supersedes_id must identify another memory")
	}
	if m.TaskID != nil && strings.TrimSpace(string(*m.TaskID)) == "" {
		return invalid("task_id must not be empty")
	}
	if m.SessionID != nil && strings.TrimSpace(string(*m.SessionID)) == "" {
		return invalid("session_id must not be empty")
	}
	return nil
}

// MemorySearchQuery filters active, unexpired memories before deterministic ranking.
type MemorySearchQuery struct {
	Query         string       `json:"query,omitempty"`
	Scope         MemoryScope  `json:"scope,omitempty"`
	ScopeID       string       `json:"scope_id,omitempty"`
	Types         []MemoryType `json:"types,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	MinImportance int          `json:"min_importance,omitempty"`
	Limit         int          `json:"limit,omitempty"`
	At            time.Time    `json:"-"`
}

func (q MemorySearchQuery) Validate() error {
	if q.Scope != "" && q.Scope != MemoryScopeGlobal && q.Scope != MemoryScopeTask && q.Scope != MemoryScopeSession {
		return fmt.Errorf("%w: invalid scope filter %q", ErrInvalidMemory, q.Scope)
	}
	if q.Scope == MemoryScopeGlobal && q.ScopeID != "" {
		return fmt.Errorf("%w: global scope filter cannot have a scope ID", ErrInvalidMemory)
	}
	if q.Scope != "" && q.Scope != MemoryScopeGlobal && strings.TrimSpace(q.ScopeID) == "" {
		return fmt.Errorf("%w: scope ID is required", ErrInvalidMemory)
	}
	if q.MinImportance < 0 || q.MinImportance > 5 {
		return fmt.Errorf("%w: minimum importance must be between 0 and 5", ErrInvalidMemory)
	}
	if q.Limit < 0 || q.Limit > 100 {
		return fmt.Errorf("%w: limit must be between 0 and 100", ErrInvalidMemory)
	}
	for _, t := range q.Types {
		if t != MemoryWorking && t != MemoryEpisodic && t != MemorySemantic {
			return fmt.Errorf("%w: invalid memory type %q", ErrInvalidMemory, t)
		}
	}
	for _, tag := range q.Tags {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("%w: empty tag filter", ErrInvalidMemory)
		}
	}
	return nil
}

func MemoryMetadataOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}
