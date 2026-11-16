package domain

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

func TestMemoryValidationAndWorkingExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := Memory{ID: "mem-1", Scope: MemoryScopeGlobal, Type: MemorySemantic, Content: "The repository uses SQLite", Source: MemorySourceUser, Confidence: 1, Importance: 3, Metadata: json.RawMessage(`{"verified":true}`), CreatedAt: now, UpdatedAt: now}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid memory rejected: %v", err)
	}
	working := valid
	working.Type = MemoryWorking
	if err := working.Validate(); !errors.Is(err, ErrInvalidMemory) {
		t.Fatalf("working without expiry error=%v", err)
	}
	expires := now.Add(time.Hour)
	working.ExpiresAt = &expires
	if err := working.Validate(); err != nil {
		t.Fatalf("working with expiry rejected: %v", err)
	}
	bad := valid
	bad.Scope = MemoryScopeTask
	if err := bad.Validate(); !errors.Is(err, ErrInvalidMemory) {
		t.Fatalf("invalid scope error=%v", err)
	}
	bad = valid
	bad.Confidence = 1.1
	if err := bad.Validate(); !errors.Is(err, ErrInvalidMemory) {
		t.Fatalf("confidence error=%v", err)
	}
	bad = valid
	bad.Confidence = math.NaN()
	if err := bad.Validate(); !errors.Is(err, ErrInvalidMemory) {
		t.Fatalf("NaN confidence error=%v", err)
	}
	bad = valid
	bad.Metadata = json.RawMessage(`[]`)
	if err := bad.Validate(); !errors.Is(err, ErrInvalidMemory) {
		t.Fatalf("metadata error=%v", err)
	}
	bad = valid
	archived := now.Add(time.Hour)
	bad.ArchivedAt = &archived
	if err := bad.Validate(); !errors.Is(err, ErrInvalidMemory) {
		t.Fatalf("archive timestamp error=%v", err)
	}
}

func TestTaskAndSessionScopedMemoryValidation(t *testing.T) {
	now := time.Now().UTC()
	taskID := TaskID("task-1")
	task := Memory{ID: "m1", Scope: MemoryScopeTask, ScopeID: string(taskID), Type: MemoryEpisodic, Content: "worked", Source: MemorySourceAgent, Confidence: .8, Importance: 2, TaskID: &taskID, CreatedAt: now, UpdatedAt: now}
	if err := task.Validate(); err != nil {
		t.Fatal(err)
	}
	sessionID := SessionID("sess-1")
	session := task
	session.ID = "m2"
	session.Scope = MemoryScopeSession
	session.ScopeID = string(sessionID)
	session.TaskID = nil
	session.SessionID = &sessionID
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	session.ScopeID = "other"
	if err := session.Validate(); !errors.Is(err, ErrInvalidMemory) {
		t.Fatalf("mismatched session scope error=%v", err)
	}
}
