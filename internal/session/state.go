package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

var (
	ErrInvalidDependency      = errors.New("session manager dependency is nil")
	ErrTaskSessionMismatch    = errors.New("task does not match session")
	ErrUnsupportedCapability  = errors.New("provider capability is unsupported")
	ErrInvalidProviderSession = errors.New("provider returned an invalid session reference")
)

type sessionLockEntry struct {
	token chan struct{}
	refs  int
}

// sessionLocker serializes operations for one session without blocking
// unrelated sessions. Waiting for a lock respects the caller's context.
type sessionLocker struct {
	mu      sync.Mutex
	entries map[domain.SessionID]*sessionLockEntry
}

func newSessionLocker() *sessionLocker {
	return &sessionLocker{entries: make(map[domain.SessionID]*sessionLockEntry)}
}

func (l *sessionLocker) acquire(ctx context.Context, id domain.SessionID) (func(), error) {
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}

	l.mu.Lock()
	entry := l.entries[id]
	if entry == nil {
		entry = &sessionLockEntry{token: make(chan struct{}, 1)}
		l.entries[id] = entry
	}
	entry.refs++
	l.mu.Unlock()

	select {
	case entry.token <- struct{}{}:
		unlock := func() {
			<-entry.token
			l.releaseRef(id, entry)
		}
		if err := ctx.Err(); err != nil {
			unlock()
			return nil, err
		}
		return unlock, nil
	case <-ctx.Done():
		l.releaseRef(id, entry)
		return nil, ctx.Err()
	}
}

func (l *sessionLocker) releaseRef(id domain.SessionID, entry *sessionLockEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.refs--
	if entry.refs == 0 && l.entries[id] == entry {
		delete(l.entries, id)
	}
}

func newSessionID() (domain.SessionID, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return domain.SessionID("ses_" + hex.EncodeToString(id[:])), nil
}
