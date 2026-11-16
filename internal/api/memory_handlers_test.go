package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/memory"
)

type memoryManagerFake struct {
	values map[domain.MemoryID]domain.Memory
}

func (f *memoryManagerFake) Store(_ context.Context, m domain.Memory) (domain.Memory, error) {
	m.ID = "memory-1"
	f.values[m.ID] = m
	return m, nil
}
func (f *memoryManagerFake) Get(_ context.Context, id domain.MemoryID) (domain.Memory, error) {
	m, ok := f.values[id]
	if !ok {
		return domain.Memory{}, domain.ErrMemoryNotFound
	}
	return m, nil
}
func (f *memoryManagerFake) Update(_ context.Context, m domain.Memory) (domain.Memory, error) {
	f.values[m.ID] = m
	return m, nil
}
func (f *memoryManagerFake) Search(_ context.Context, q domain.MemorySearchQuery) ([]memory.Result, error) {
	out := []memory.Result{}
	for _, m := range f.values {
		out = append(out, memory.Result{Memory: m, Score: 1, Reasons: []string{"test"}})
	}
	return out, nil
}
func (f *memoryManagerFake) Archive(_ context.Context, id domain.MemoryID) (domain.Memory, error) {
	m, ok := f.values[id]
	if !ok {
		return domain.Memory{}, domain.ErrMemoryNotFound
	}
	return m, nil
}
func (f *memoryManagerFake) Delete(_ context.Context, id domain.MemoryID) error {
	if _, ok := f.values[id]; !ok {
		return domain.ErrMemoryNotFound
	}
	delete(f.values, id)
	return nil
}

// Exercise the routed API with injected dependencies, keeping persistence behind the manager interface.
func TestMemoryRESTRoutes(t *testing.T) {
	manager := &memoryManagerFake{values: map[domain.MemoryID]domain.Memory{}}
	coordinator := mustCoordinator(t, &fakeCoordinator{session: validSession("session-1", domain.SessionRunning)})
	server, err := NewServer(Config{}, Dependencies{Coordinator: coordinator, Sessions: &fakeSessions{values: map[domain.SessionID]domain.Session{}}, Events: newFakeEvents(), ExecutionContext: context.Background(), Memories: manager})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		return res
	}
	created := call(http.MethodPost, "/api/v1/memories", `{"content":"remember this","scope":"global","type":"semantic","source":"user"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body)
	}
	var envelope memoryEnvelope
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Memory.ID != "memory-1" || envelope.Memory.Content != "remember this" {
		t.Fatalf("created=%#v", envelope)
	}
	if response := call(http.MethodPost, "/api/v1/memories/search", `{"query":"remember"}`); response.Code != http.StatusOK {
		t.Fatalf("search=%d %s", response.Code, response.Body)
	}
	if response := call(http.MethodGet, "/api/v1/memories/memory-1", ""); response.Code != http.StatusOK {
		t.Fatalf("get=%d %s", response.Code, response.Body)
	}
	if response := call(http.MethodPatch, "/api/v1/memories/memory-1", `{"content":"updated"}`); response.Code != http.StatusOK {
		t.Fatalf("patch=%d %s", response.Code, response.Body)
	}
	if response := call(http.MethodPost, "/api/v1/memories/memory-1/archive", ""); response.Code != http.StatusOK {
		t.Fatalf("archive=%d %s", response.Code, response.Body)
	}
	if response := call(http.MethodDelete, "/api/v1/memories/memory-1", ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", response.Code, response.Body)
	}
}
