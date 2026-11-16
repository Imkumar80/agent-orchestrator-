// Package sqlite implements the session repository using SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/migrations"
	_ "modernc.org/sqlite"
)

const (
	defaultBusyTimeout = 5 * time.Second
	defaultMaxOpen     = 4
	timestampLayout    = "2006-01-02T15:04:05.000000000Z07:00"
)

var (
	ErrDuplicateSession     = errors.New("session already exists")
	ErrStaleSessionUpdate   = errors.New("session update is older than stored state")
	ErrInvalidMigrationName = errors.New("invalid SQLite migration filename")
)

var _ ports.SessionRepository = (*Repository)(nil)
var _ ports.SessionLister = (*Repository)(nil)
var _ ports.StartupLeaseStore = (*Repository)(nil)

// Config contains settings for one local SQLite database.
type Config struct {
	// Path is the database file path. The special value ":memory:" is supported
	// for tests and keeps a single pooled connection.
	Path string

	// BusyTimeout bounds SQLite lock waits. Zero selects five seconds.
	BusyTimeout time.Duration

	// MaxOpenConns bounds the database/sql pool. Zero selects four connections.
	MaxOpenConns int
}

// Repository stores domain sessions without exposing SQLite types to callers.
type Repository struct {
	db *sql.DB
}

// Open creates the parent directory when needed, opens SQLite, applies
// connection settings, and runs pending embedded migrations.
func Open(ctx context.Context, config Config) (*Repository, error) {
	if ctx == nil {
		return nil, errors.New("SQLite context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Path) == "" {
		return nil, errors.New("SQLite database path is required")
	}
	if config.BusyTimeout < 0 {
		return nil, errors.New("SQLite busy timeout must not be negative")
	}
	if config.BusyTimeout == 0 {
		config.BusyTimeout = defaultBusyTimeout
	}
	if config.MaxOpenConns < 0 {
		return nil, errors.New("SQLite max open connections must not be negative")
	}
	if config.MaxOpenConns == 0 {
		config.MaxOpenConns = defaultMaxOpen
	}

	path := config.Path
	if path != ":memory:" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve SQLite database path: %w", err)
		}
		path = absPath
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create SQLite database directory: %w", err)
		}
		if err := createDatabaseFile(path); err != nil {
			return nil, err
		}
	}

	if path == ":memory:" {
		config.MaxOpenConns = 1
	}
	dsn := makeDSN(path, config.BusyTimeout)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxOpenConns)
	failed := true
	defer func() {
		if failed {
			_ = db.Close()
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to SQLite database: %w", err)
	}
	if path != ":memory:" {
		var journalMode string
		if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
			return nil, fmt.Errorf("enable SQLite WAL mode: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return nil, fmt.Errorf("enable SQLite WAL mode: SQLite selected %q", journalMode)
		}
	}
	if err := applyMigrations(ctx, db); err != nil {
		return nil, fmt.Errorf("migrate SQLite database: %w", err)
	}
	failed = false
	return &Repository{db: db}, nil
}

func createDatabaseFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close newly created SQLite database file: %w", closeErr)
		}
		return nil
	}
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("inspect SQLite database file: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("SQLite database path %q is not a regular file", path)
		}
		return nil
	}
	return fmt.Errorf("create SQLite database file: %w", err)
}

func makeDSN(path string, busyTimeout time.Duration) string {
	values := url.Values{}
	values.Add("_pragma", "busy_timeout="+strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	values.Add("_pragma", "foreign_keys=1")
	if path == ":memory:" {
		values.Set("mode", "memory")
		values.Set("cache", "shared")
		return "file:orchestrator-memory?" + values.Encode()
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: values.Encode()}
	return uri.String()
}

// Close shuts down the connection pool. It is safe to call more than once.
func (r *Repository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	if err := r.db.Close(); err != nil {
		return fmt.Errorf("close SQLite database: %w", err)
	}
	return nil
}

// Create persists a validated session. Duplicate IDs return ErrDuplicateSession.
func (r *Repository) Create(ctx context.Context, session domain.Session) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session before SQLite create: %w", err)
	}
	args := sessionArgs(session)
	result, err := r.db.ExecContext(ctx, `
INSERT INTO sessions (
    id, task_id, provider, native_provider_session_id, status,
    created_at, updated_at, error_code, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`, args...)
	if err != nil {
		return fmt.Errorf("create SQLite session %q: %w", session.ID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check SQLite session create %q: %w", session.ID, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: %q", ErrDuplicateSession, session.ID)
	}
	return nil
}

// Get loads and validates a session. Missing IDs wrap domain.ErrSessionNotFound.
func (r *Repository) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	if err := r.check(ctx); err != nil {
		return domain.Session{}, err
	}
	value, err := scanSession(r.db.QueryRowContext(ctx, `
SELECT id, task_id, provider, native_provider_session_id, status,
       created_at, updated_at, error_code, error_message
FROM sessions WHERE id = ?`, string(id)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, fmt.Errorf("get SQLite session %q: %w", id, domain.ErrSessionNotFound)
		}
		return domain.Session{}, fmt.Errorf("get SQLite session %q: %w", id, err)
	}
	return value, nil
}

// List returns every validated persisted session ordered by creation time and ID.
func (r *Repository) List(ctx context.Context) ([]domain.Session, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, task_id, provider, native_provider_session_id, status, created_at, updated_at, error_code, error_message FROM sessions ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list SQLite sessions: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Session, 0)
	for rows.Next() {
		var id, taskID, provider, nativeID, status, createdAt, updatedAt string
		var errorCode, errorMessage sql.NullString
		if err := rows.Scan(&id, &taskID, &provider, &nativeID, &status, &createdAt, &updatedAt, &errorCode, &errorMessage); err != nil {
			return result, fmt.Errorf("scan SQLite session during list: %w", err)
		}
		created, err := parseTime(createdAt)
		if err != nil {
			return result, fmt.Errorf("session %q created_at: %w", id, err)
		}
		updated, err := parseTime(updatedAt)
		if err != nil {
			return result, fmt.Errorf("session %q updated_at: %w", id, err)
		}
		if errorCode.Valid != errorMessage.Valid {
			return result, fmt.Errorf("session %q has incomplete error information", id)
		}
		session := domain.Session{ID: domain.SessionID(id), TaskID: domain.TaskID(taskID), Provider: domain.AgentProvider(provider), NativeProviderSessionID: nativeID, Status: domain.SessionStatus(status), CreatedAt: created, UpdatedAt: updated}
		if errorCode.Valid {
			session.Error = &domain.ErrorInfo{Code: errorCode.String, Message: errorMessage.String}
		}
		if err := session.Validate(); err != nil {
			return result, fmt.Errorf("validate listed session %q: %w", id, err)
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("read SQLite sessions: %w", err)
	}
	return result, nil
}

// AcquireStartupLease atomically claims the singleton daemon lease when vacant,
// expired, or already owned by the caller.
func (r *Repository) AcquireStartupLease(ctx context.Context, owner string, now, expires time.Time) (bool, error) {
	return r.writeLease(ctx, `INSERT INTO daemon_lease(singleton, owner_id, expires_at) VALUES (1, ?, ?) ON CONFLICT(singleton) DO UPDATE SET owner_id=excluded.owner_id, expires_at=excluded.expires_at WHERE daemon_lease.expires_at <= ? OR daemon_lease.owner_id = ?`, owner, expires, now, owner)
}

// RenewStartupLease extends a lease only while this owner still holds it.
func (r *Repository) RenewStartupLease(ctx context.Context, owner string, now, expires time.Time) (bool, error) {
	return r.writeLease(ctx, `UPDATE daemon_lease SET expires_at=? WHERE singleton=1 AND owner_id=? AND expires_at > ?`, expires, owner, now)
}

// ReleaseStartupLease releases only the lease belonging to owner.
func (r *Repository) ReleaseStartupLease(ctx context.Context, owner string) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM daemon_lease WHERE singleton=1 AND owner_id=?`, owner); err != nil {
		return fmt.Errorf("release SQLite startup lease: %w", err)
	}
	return nil
}

func (r *Repository) writeLease(ctx context.Context, query string, args ...any) (bool, error) {
	if err := r.check(ctx); err != nil {
		return false, err
	}
	values := make([]any, 0, len(args))
	for _, arg := range args {
		if value, ok := arg.(time.Time); ok {
			values = append(values, formatTime(value))
		} else {
			values = append(values, arg)
		}
	}
	result, err := r.db.ExecContext(ctx, query, values...)
	if err != nil {
		return false, fmt.Errorf("update SQLite startup lease: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect SQLite startup lease: %w", err)
	}
	return count > 0, nil
}

// Update replaces a session if its update timestamp is not older than the
// stored version. The repository contract has no revision token, so equal
// timestamps cannot provide full optimistic concurrency protection.
func (r *Repository) Update(ctx context.Context, session domain.Session) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session before SQLite update: %w", err)
	}
	args := sessionArgs(session)
	result, err := r.db.ExecContext(ctx, `
UPDATE sessions SET
    task_id = ?, provider = ?, native_provider_session_id = ?, status = ?,
    created_at = ?, updated_at = ?, error_code = ?, error_message = ?
WHERE id = ? AND updated_at <= ?`,
		args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8],
		string(session.ID), formatTime(session.UpdatedAt))
	if err != nil {
		return fmt.Errorf("update SQLite session %q: %w", session.ID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check SQLite session update %q: %w", session.ID, err)
	}
	if rows != 0 {
		return nil
	}
	var storedUpdatedAt string
	if err := r.db.QueryRowContext(ctx, "SELECT updated_at FROM sessions WHERE id = ?", string(session.ID)).Scan(&storedUpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("update SQLite session %q: %w", session.ID, domain.ErrSessionNotFound)
		}
		return fmt.Errorf("inspect SQLite session %q after update: %w", session.ID, err)
	}
	if storedUpdatedAt > formatTime(session.UpdatedAt) {
		return fmt.Errorf("update SQLite session %q: %w", session.ID, ErrStaleSessionUpdate)
	}
	return fmt.Errorf("update SQLite session %q: no row was changed", session.ID)
}

func (r *Repository) check(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("SQLite repository is nil or closed")
	}
	if ctx == nil {
		return errors.New("SQLite context must not be nil")
	}
	return ctx.Err()
}

func sessionArgs(session domain.Session) []any {
	var errorCode, errorMessage any
	if session.Error != nil {
		errorCode = session.Error.Code
		errorMessage = session.Error.Message
	}
	return []any{
		string(session.ID), string(session.TaskID), string(session.Provider),
		session.NativeProviderSessionID, string(session.Status),
		formatTime(session.CreatedAt), formatTime(session.UpdatedAt),
		errorCode, errorMessage,
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(timestampLayout)
}

func scanSession(row *sql.Row) (domain.Session, error) {
	var (
		id, taskID, provider, nativeID, status string
		createdAt, updatedAt                   string
		errorCode, errorMessage                sql.NullString
	)
	if err := row.Scan(&id, &taskID, &provider, &nativeID, &status, &createdAt, &updatedAt, &errorCode, &errorMessage); err != nil {
		return domain.Session{}, err
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse session created_at %q: %w", createdAt, err)
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse session updated_at %q: %w", updatedAt, err)
	}
	if errorCode.Valid != errorMessage.Valid {
		return domain.Session{}, errors.New("stored session has incomplete error information")
	}
	session := domain.Session{
		ID:                      domain.SessionID(id),
		TaskID:                  domain.TaskID(taskID),
		Provider:                domain.AgentProvider(provider),
		NativeProviderSessionID: nativeID,
		Status:                  domain.SessionStatus(status),
		CreatedAt:               created,
		UpdatedAt:               updated,
	}
	if errorCode.Valid {
		session.Error = &domain.ErrorInfo{Code: errorCode.String, Message: errorMessage.String}
	}
	if err := session.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate stored session: %w", err)
	}
	return session, nil
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(timestampLayout, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

type migration struct {
	version int
	name    string
	content string
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema migration tracking table: %w", err)
	}
	available, err := readMigrations()
	if err != nil {
		return err
	}
	applied := make(map[int]string)
	rows, err := db.QueryContext(ctx, "SELECT version, name FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = name
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read applied migrations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close applied migrations query: %w", err)
	}
	for version, name := range applied {
		found := false
		for _, item := range available {
			if item.version == version {
				found = true
				if item.name != name {
					return fmt.Errorf("migration %d name mismatch: database has %q, source has %q", version, name, item.name)
				}
			}
		}
		if !found {
			return fmt.Errorf("database has unknown migration version %d (%s)", version, name)
		}
	}

	for index, item := range available {
		if _, exists := applied[item.version]; exists {
			continue
		}
		if item.version != index+1 {
			return fmt.Errorf("migration sequence gap: found version %d at position %d", item.version, index+1)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %04d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, item.content); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration %04d %s: %w", item.version, item.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
			item.version, item.name, formatTime(time.Now())); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %04d %s: %w", item.version, item.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %04d %s: %w", item.version, item.name, err)
		}
	}
	return nil
}

func readMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migration directory: %w", err)
	}
	items := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := entry.Name()
		if len(name) < 6 || name[4] != '_' {
			return nil, fmt.Errorf("%w: %q", ErrInvalidMigrationName, name)
		}
		version, err := strconv.Atoi(name[:4])
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("%w: %q", ErrInvalidMigrationName, name)
		}
		content, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		items = append(items, migration{version: version, name: name, content: string(content)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].version < items[j].version })
	for i := 1; i < len(items); i++ {
		if items[i-1].version == items[i].version {
			return nil, fmt.Errorf("duplicate migration version %d", items[i].version)
		}
	}
	return items, nil
}
