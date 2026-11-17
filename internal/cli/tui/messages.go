package tui

import (
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

type dashboardLoadedMsg struct {
	requestID uint64
	health    client.HealthResponse
	sessions  []domain.Session
	providers []client.Provider
	reachable bool
	err       error
}

type taskProvidersLoadedMsg struct {
	requestID uint64
	providers []client.Provider
	err       error
}

type taskSubmittedMsg struct {
	requestID uint64
	response  client.TaskResponse
	err       error
}

type sessionsLoadedMsg struct {
	requestID uint64
	sessions  []domain.Session
	err       error
}

type sessionDetailLoadedMsg struct {
	requestID uint64
	session   domain.Session
	connect   bool
	err       error
}

type detailStreamStartedMsg struct{ streamID uint64 }

type detailStreamEventMsg struct {
	streamID uint64
	event    client.Event
}

type detailStreamEndedMsg struct {
	streamID uint64
	err      error
	canceled bool
}

type sessionCancelResultMsg struct {
	requestID uint64
	response  client.CancelResponse
	err       error
}

type refreshTickMsg struct{}
