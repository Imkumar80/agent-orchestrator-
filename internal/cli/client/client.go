package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

const DefaultBaseURL = "http://127.0.0.1:8080"

type Client struct {
	base string
	http *http.Client
}

func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.base
}

func NewTaskID() (domain.TaskID, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate task ID: %w", err)
	}
	return domain.TaskID("task_" + hex.EncodeToString(value[:])), nil
}

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("server returned HTTP %d (%s): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("server returned HTTP %d", e.Status)
}

func New(base string) (*Client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid server URL %q", base)
	}
	if u.User != nil {
		return nil, errors.New("server URL must not contain credentials")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 10 * time.Second
	return &Client{base: base, http: &http.Client{Transport: transport}}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any, target any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode API request: %w", err)
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, input)
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope)
		return &APIError{Status: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	if target == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}
	return nil
}

type TaskResponse struct {
	Accepted bool           `json:"accepted"`
	Session  domain.Session `json:"session"`
}
type SessionResponse struct {
	Session domain.Session `json:"session"`
}
type CancelResponse struct {
	Accepted        bool           `json:"accepted"`
	AlreadyTerminal bool           `json:"already_terminal"`
	Session         domain.Session `json:"session"`
}
type SessionsResponse struct {
	Sessions []domain.Session `json:"sessions"`
}
type Provider struct {
	Name      domain.AgentProvider `json:"name"`
	Available bool                 `json:"available"`
}
type ProvidersResponse struct {
	Providers []Provider `json:"providers"`
}
type HealthResponse struct {
	Status string `json:"status"`
}
type Event struct {
	ID        string                `json:"id"`
	SessionID domain.SessionID      `json:"session_id"`
	Type      domain.AgentEventType `json:"type"`
	Timestamp time.Time             `json:"timestamp"`
	Payload   json.RawMessage       `json:"payload"`
}

func (c *Client) CreateTask(ctx context.Context, task domain.Task) (TaskResponse, error) {
	var out TaskResponse
	body := map[string]any{"task": map[string]any{"id": task.ID, "description": task.Description, "provider": task.Provider, "working_directory": task.WorkingDirectory}}
	err := c.request(ctx, http.MethodPost, "/api/v1/tasks", body, &out)
	return out, err
}
func (c *Client) GetSession(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	var out SessionResponse
	err := c.request(ctx, http.MethodGet, "/api/v1/sessions/"+url.PathEscape(string(id)), nil, &out)
	return out.Session, err
}
func (c *Client) CancelSession(ctx context.Context, id domain.SessionID) (CancelResponse, error) {
	var out CancelResponse
	err := c.request(ctx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(string(id))+"/cancel", nil, &out)
	return out, err
}
func (c *Client) ListSessions(ctx context.Context) ([]domain.Session, error) {
	var out SessionsResponse
	err := c.request(ctx, http.MethodGet, "/api/v1/sessions", nil, &out)
	return out.Sessions, err
}
func (c *Client) ListProviders(ctx context.Context) ([]Provider, error) {
	var out ProvidersResponse
	err := c.request(ctx, http.MethodGet, "/api/v1/providers", nil, &out)
	return out.Providers, err
}
func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	var out HealthResponse
	err := c.request(ctx, http.MethodGet, "/health", nil, &out)
	return out, err
}

// StreamEvents consumes the existing SSE endpoint until cancellation or disconnect.
func (c *Client) StreamEvents(ctx context.Context, id domain.SessionID, consume func(Event) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/sessions/"+url.PathEscape(string(id))+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connect event stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope)
		return &APIError{Status: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("server returned %q instead of an event stream", resp.Header.Get("Content-Type"))
	}
	return readSSE(ctx, resp.Body, consume)
}
