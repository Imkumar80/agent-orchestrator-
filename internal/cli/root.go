package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/app"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

const Version = "0.1.0"

type Runner struct {
	Out, Err  io.Writer
	LookupEnv func(string) (string, bool)
}

func (r Runner) Run(ctx context.Context, args []string) error {
	if r.Out == nil {
		r.Out = os.Stdout
	}
	if r.Err == nil {
		r.Err = os.Stderr
	}
	if r.LookupEnv == nil {
		r.LookupEnv = os.LookupEnv
	}
	server := client.DefaultBaseURL
	if value, ok := r.LookupEnv("ORCHESTRATOR_SERVER_URL"); ok && strings.TrimSpace(value) != "" {
		server = strings.TrimSpace(value)
	}
	if len(args) > 0 && args[0] == "--server" {
		if len(args) < 2 {
			return errors.New("--server requires a URL")
		}
		server = args[1]
		args = args[2:]
	} else if len(args) > 0 && strings.HasPrefix(args[0], "--server=") {
		server = strings.TrimPrefix(args[0], "--server=")
		args = args[1:]
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		r.printHelp()
		return nil
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintf(r.Out, "agent-orchestrator %s\n", Version)
		return nil
	}
	if args[0] == "serve" {
		return app.Run(ctx)
	}
	apiClient, err := client.New(server)
	if err != nil {
		return err
	}
	switch args[0] {
	case "task":
		return r.task(ctx, apiClient, args[1:])
	case "session":
		return r.session(ctx, apiClient, args[1:])
	case "provider":
		return r.provider(ctx, apiClient, args[1:])
	case "doctor":
		return r.doctor(ctx, apiClient)
	default:
		r.printHelp()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (r Runner) printHelp() {
	fmt.Fprintln(r.Out, `Agent Orchestrator

Usage: agent-orchestrator [--server URL] <command>

Commands:
  serve                         Start the orchestration HTTP server
  task create                   Create and start a task
  session get <session_id>      Show a session
  session cancel <session_id>  Interrupt a session
  session list                  List sessions
  session logs <session_id>     Stream live session events
  provider list                 Show provider availability
  doctor                        Check server reachability
  version                       Show CLI version

Server URL: --server URL or ORCHESTRATOR_SERVER_URL (default http://127.0.0.1:8080)
Events are live-only and cannot be replayed after disconnection or restart.`)
}

func (r Runner) task(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: agent-orchestrator task create --description TEXT [--provider NAME] [--workdir DIR] [--json]")
	}
	fs := flag.NewFlagSet("task create", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	description := fs.String("description", "", "task description")
	provider := fs.String("provider", "codex", "agent provider")
	workdir := fs.String("workdir", ".", "working directory")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected task create argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(*description) == "" {
		return errors.New("--description is required")
	}
	name := domain.AgentProvider(*provider)
	if err := name.Validate(); err != nil {
		return err
	}
	abs, err := filepath.Abs(*workdir)
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("--workdir must point to a directory")
	}
	id, err := client.NewTaskID()
	if err != nil {
		return err
	}
	task := domain.Task{ID: id, Description: *description, Provider: name, WorkingDirectory: abs, CreatedAt: time.Now().UTC()}
	resp, err := c.CreateTask(ctx, task)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(r.Out, resp)
	}
	fmt.Fprintln(r.Out, "Task accepted. Execution may still be in progress.")
	fmt.Fprintf(r.Out, "\nSession ID : %s\nProvider   : %s\nStatus     : %s\n\nStream events:\nagent-orchestrator session logs %s\n", resp.Session.ID, resp.Session.Provider, resp.Session.Status, resp.Session.ID)
	return nil
}

func (r Runner) session(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agent-orchestrator session <get|cancel|list|logs>")
	}
	switch args[0] {
	case "list":
		if len(args) > 2 {
			return errors.New("usage: agent-orchestrator session list [--json]")
		}
		items, err := c.ListSessions(ctx)
		if err != nil {
			return err
		}
		if len(args) > 1 && args[1] == "--json" {
			return writeJSON(r.Out, items)
		}
		if len(args) > 1 {
			return fmt.Errorf("unknown session list option %q", args[1])
		}
		if len(items) == 0 {
			fmt.Fprintln(r.Out, "No sessions.")
			return nil
		}
		fmt.Fprintf(r.Out, "%-28s %-10s %-20s %s\n", "SESSION ID", "PROVIDER", "STATUS", "UPDATED")
		for _, item := range items {
			fmt.Fprintf(r.Out, "%-28s %-10s %-20s %s\n", item.ID, item.Provider, item.Status, item.UpdatedAt.Local().Format(time.RFC3339))
		}
		return nil
	case "get":
		if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--json") {
			return errors.New("usage: agent-orchestrator session get <session_id> [--json]")
		}
		s, err := c.GetSession(ctx, domain.SessionID(args[1]))
		if err != nil {
			return err
		}
		if len(args) == 3 {
			return writeJSON(r.Out, s)
		}
		fmt.Fprintf(r.Out, "Session: %s\n\nProvider     %s\nStatus       %s\nCreated      %s\nUpdated      %s\n", s.ID, s.Provider, s.Status, s.CreatedAt.Format(time.RFC3339), s.UpdatedAt.Format(time.RFC3339))
		if s.Status.Terminal() {
			fmt.Fprintln(r.Out, "\nThis session is terminal.")
		}
		return nil
	case "cancel":
		if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--json") {
			return errors.New("usage: agent-orchestrator session cancel <session_id> [--json]")
		}
		result, err := c.CancelSession(ctx, domain.SessionID(args[1]))
		if err != nil {
			return err
		}
		if len(args) == 3 {
			return writeJSON(r.Out, result)
		}
		if result.AlreadyTerminal {
			fmt.Fprintf(r.Out, "Session %s is already terminal (%s).\n", result.Session.ID, result.Session.Status)
		} else {
			fmt.Fprintf(r.Out, "Cancellation accepted for session %s (%s).\n", result.Session.ID, result.Session.Status)
		}
		return nil
	case "logs":
		if len(args) != 2 {
			return errors.New("usage: agent-orchestrator session logs <session_id>")
		}
		fmt.Fprintln(r.Err, "Streaming live events; events are not replayable. Press Ctrl+C to stop.")
		err := c.StreamEvents(ctx, domain.SessionID(args[1]), func(event client.Event) error {
			stamp := event.Timestamp
			if stamp.IsZero() {
				stamp = time.Now()
			}
			payload := strings.TrimSpace(string(event.Payload))
			if payload != "" && payload != "null" {
				fmt.Fprintf(r.Out, "[%s] %s %s\n", stamp.Local().Format("15:04:05"), event.Type, payload)
			} else {
				fmt.Fprintf(r.Out, "[%s] %s\n", stamp.Local().Format("15:04:05"), event.Type)
			}
			return nil
		})
		if err == nil {
			fmt.Fprintln(r.Out, "Event stream ended.")
		}
		return err
	default:
		return fmt.Errorf("unknown session command %q", args[0])
	}
}

func (r Runner) provider(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 || args[0] != "list" || len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
		return errors.New("usage: agent-orchestrator provider list [--json]")
	}
	items, err := c.ListProviders(ctx)
	if err != nil {
		return err
	}
	if len(args) > 1 && args[1] == "--json" {
		return writeJSON(r.Out, items)
	}
	for _, item := range items {
		status := "unavailable (not configured)"
		if item.Available {
			status = "available"
		}
		fmt.Fprintf(r.Out, "%-10s %s\n", item.Name, status)
	}
	return nil
}

func (r Runner) doctor(ctx context.Context, c *client.Client) error {
	fmt.Fprintf(r.Out, "Server URL: %s\n", c.BaseURL())
	response, err := c.Health(ctx)
	if err != nil {
		return fmt.Errorf("server health check failed: %w", err)
	}
	fmt.Fprintf(r.Out, "Health: %s (HTTP process liveness; provider readiness is not checked)\n", response.Status)
	return nil
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
