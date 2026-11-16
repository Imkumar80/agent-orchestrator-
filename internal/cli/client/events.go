package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"io"
	"strings"
)

func readSSE(ctx context.Context, body io.Reader, consume func(Event) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var eventName string
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		var event Event
		if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
			return fmt.Errorf("decode streamed event: %w", err)
		}
		if event.Type == "" {
			event.Type = domainEventType(eventName)
		}
		data.Reset()
		eventName = ""
		if consume != nil {
			return consume(event)
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read event stream: %w", err)
	}
	return flush()
}

// Kept separate so SSE event names remain a transport fallback, not domain state.
func domainEventType(value string) domain.AgentEventType { return domain.AgentEventType(value) }
