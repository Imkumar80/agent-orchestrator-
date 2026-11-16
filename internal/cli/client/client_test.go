package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientSuccessErrorsInvalidJSONAndBodyClose(t *testing.T) {
	closed := false
	c := &Client{base: "http://test", http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/ok" {
			return &http.Response{StatusCode: 200, Body: closeReader{Reader: strings.NewReader(`{"status":"ok"}`), closed: &closed}, Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 422, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"unsupported_provider","message":"provider unavailable"}}`)), Header: make(http.Header)}, nil
	})}}
	var out HealthResponse
	if err := c.request(context.Background(), http.MethodGet, "/ok", nil, &out); err != nil || out.Status != "ok" || !closed {
		t.Fatalf("response=%+v closed=%v err=%v", out, closed, err)
	}
	var apiErr *APIError
	if err := c.request(context.Background(), http.MethodGet, "/bad", nil, nil); !errors.As(err, &apiErr) || apiErr.Status != 422 || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("HTTP error=%v", err)
	}
	bad := &Client{base: "http://test", http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not-json")), Header: make(http.Header)}, nil
	})}}
	if err := bad.request(context.Background(), http.MethodGet, "/", nil, &out); err == nil {
		t.Fatal("invalid JSON should fail")
	}
}

func TestClientNetworkFailureAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	c, _ := New(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Health(ctx); done <- err }()
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled request succeeded")
	}
	server.Close()
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("closed server should fail")
	}
}

func TestStreamEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: provider.output\ndata: {\"id\":\"e1\",\"session_id\":\"s1\",\"type\":\"provider.output\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"payload\":{}}\n\n")
	}))
	defer server.Close()
	c, _ := New(server.URL)
	got := 0
	err := c.StreamEvents(context.Background(), "s1", func(event Event) error {
		got++
		if event.Type != "provider.output" || event.ID != "e1" {
			t.Errorf("event=%+v", event)
		}
		return nil
	})
	if err != nil || got != 1 {
		t.Fatalf("got=%d err=%v", got, err)
	}
}

func TestNewRejectsInvalidURLAndGeneratesTaskID(t *testing.T) {
	if _, err := New("not a url"); err == nil {
		t.Fatal("invalid URL accepted")
	}
	if _, err := New("http://user:secret@example.com"); err == nil {
		t.Fatal("credential-bearing URL accepted")
	}
	id, err := NewTaskID()
	if err != nil || id == "" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type closeReader struct {
	io.Reader
	closed *bool
}

func (r closeReader) Close() error { *r.closed = true; return nil }
