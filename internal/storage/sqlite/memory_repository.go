package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

var (
	ErrStaleMemoryUpdate                        = errors.New("memory update is older than stored state")
	_                    ports.MemoryRepository = (*Repository)(nil)
)

func (r *Repository) CreateMemory(ctx context.Context, m domain.Memory) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("validate memory before create: %w", err)
	}
	tags, err := json.Marshal(m.Tags)
	if err != nil {
		return fmt.Errorf("encode memory tags: %w", err)
	}
	metadata := domain.MemoryMetadataOrEmpty(m.Metadata)
	_, err = r.db.ExecContext(ctx, `INSERT INTO memories(id,scope,scope_id,memory_type,content,summary,source,confidence,importance,tags_json,metadata_json,task_id,session_id,supersedes_id,created_at,updated_at,expires_at,archived_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, memoryArgs(m, tags, metadata)...)
	if err != nil {
		return fmt.Errorf("create SQLite memory %q: %w", m.ID, err)
	}
	return nil
}

func (r *Repository) GetMemory(ctx context.Context, id domain.MemoryID) (domain.Memory, error) {
	if err := r.check(ctx); err != nil {
		return domain.Memory{}, err
	}
	m, err := scanMemory(r.db.QueryRowContext(ctx, memorySelect+` WHERE id=?`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Memory{}, fmt.Errorf("get SQLite memory %q: %w", id, domain.ErrMemoryNotFound)
	}
	if err != nil {
		return domain.Memory{}, fmt.Errorf("get SQLite memory %q: %w", id, err)
	}
	return m, nil
}

func (r *Repository) UpdateMemory(ctx context.Context, m domain.Memory) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("validate memory before update: %w", err)
	}
	tags, err := json.Marshal(m.Tags)
	if err != nil {
		return err
	}
	metadata := domain.MemoryMetadataOrEmpty(m.Metadata)
	args := memoryArgs(m, tags, metadata)
	updateArgs := append(append([]any(nil), args[1:]...), string(m.ID), formatTime(m.UpdatedAt))
	result, err := r.db.ExecContext(ctx, `UPDATE memories SET scope=?,scope_id=?,memory_type=?,content=?,summary=?,source=?,confidence=?,importance=?,tags_json=?,metadata_json=?,task_id=?,session_id=?,supersedes_id=?,created_at=?,updated_at=?,expires_at=?,archived_at=? WHERE id=? AND updated_at<=?`, updateArgs...)
	if err != nil {
		return fmt.Errorf("update SQLite memory %q: %w", m.ID, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var stored string
	if err := r.db.QueryRowContext(ctx, "SELECT updated_at FROM memories WHERE id=?", string(m.ID)).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("update SQLite memory %q: %w", m.ID, domain.ErrMemoryNotFound)
		}
		return fmt.Errorf("inspect SQLite memory %q: %w", m.ID, err)
	}
	if stored > formatTime(m.UpdatedAt) {
		return fmt.Errorf("update SQLite memory %q: %w", m.ID, ErrStaleMemoryUpdate)
	}
	return fmt.Errorf("update SQLite memory %q: no row changed", m.ID)
}

func (r *Repository) SearchMemories(ctx context.Context, q domain.MemorySearchQuery) ([]domain.Memory, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if q.At.IsZero() {
		q.At = time.Now().UTC()
	}
	query := memorySelect + ` WHERE archived_at IS NULL AND (expires_at IS NULL OR expires_at>?) AND NOT EXISTS (SELECT 1 FROM memories replacement WHERE replacement.supersedes_id=memories.id)`
	args := []any{formatTime(q.At)}
	if q.Scope != "" {
		query += ` AND scope=?`
		args = append(args, string(q.Scope))
		if q.ScopeID != "" {
			query += ` AND scope_id=?`
			args = append(args, q.ScopeID)
		}
	}
	if q.MinImportance > 0 {
		query += ` AND importance>=?`
		args = append(args, q.MinImportance)
	}
	if len(q.Types) > 0 {
		marks := make([]string, len(q.Types))
		for i, v := range q.Types {
			marks[i] = "?"
			args = append(args, string(v))
		}
		query += ` AND memory_type IN (` + strings.Join(marks, ",") + `)`
	}
	terms := memoryTerms(q.Query)
	if len(terms) > 0 {
		query += ` AND (`
		for i, term := range terms {
			if i > 0 {
				query += ` OR `
			}
			query += `lower(content) LIKE ? OR lower(summary) LIKE ? OR lower(tags_json) LIKE ?`
			wild := "%" + term + "%"
			args = append(args, wild, wild, wild)
		}
		query += `)`
	}
	for _, tag := range q.Tags {
		query += ` AND EXISTS (SELECT 1 FROM json_each(memories.tags_json) WHERE lower(value)=lower(?))`
		args = append(args, strings.TrimSpace(tag))
	}
	query += ` ORDER BY importance DESC, updated_at DESC, id ASC`
	if q.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, q.Limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search SQLite memories: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Memory, 0)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return out, fmt.Errorf("scan SQLite memory search result: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("read SQLite memory search results: %w", err)
	}
	return out, nil
}

func (r *Repository) ArchiveMemory(ctx context.Context, id domain.MemoryID, at time.Time) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if at.IsZero() {
		return errors.New("memory archive timestamp is required")
	}
	stamp := formatTime(at)
	result, err := r.db.ExecContext(ctx, `UPDATE memories SET archived_at=?,updated_at=CASE WHEN updated_at<? THEN ? ELSE updated_at END WHERE id=? AND archived_at IS NULL`, stamp, stamp, stamp, string(id))
	if err != nil {
		return fmt.Errorf("archive SQLite memory %q: %w", id, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var archived sql.NullString
	if err := r.db.QueryRowContext(ctx, "SELECT archived_at FROM memories WHERE id=?", string(id)).Scan(&archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("archive SQLite memory %q: %w", id, domain.ErrMemoryNotFound)
		}
		return err
	}
	return nil
}

func (r *Repository) DeleteMemory(ctx context.Context, id domain.MemoryID) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, "DELETE FROM memories WHERE id=?", string(id))
	if err != nil {
		return fmt.Errorf("delete SQLite memory %q: %w", id, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("delete SQLite memory %q: %w", id, domain.ErrMemoryNotFound)
	}
	return nil
}

func (r *Repository) ExpireWorkingMemories(ctx context.Context, now time.Time) (int64, error) {
	if err := r.check(ctx); err != nil {
		return 0, err
	}
	if now.IsZero() {
		return 0, errors.New("expiry timestamp is required")
	}
	stamp := formatTime(now)
	result, err := r.db.ExecContext(ctx, `UPDATE memories SET archived_at=?,updated_at=CASE WHEN updated_at<? THEN ? ELSE updated_at END WHERE memory_type='working' AND expires_at IS NOT NULL AND expires_at<=? AND archived_at IS NULL`, stamp, stamp, stamp, stamp)
	if err != nil {
		return 0, fmt.Errorf("expire SQLite working memories: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, nil
}

const memorySelect = `SELECT id,scope,scope_id,memory_type,content,summary,source,confidence,importance,tags_json,metadata_json,task_id,session_id,supersedes_id,created_at,updated_at,expires_at,archived_at FROM memories`

func memoryArgs(m domain.Memory, tags, metadata []byte) []any {
	return []any{string(m.ID), string(m.Scope), m.ScopeID, string(m.Type), m.Content, m.Summary, string(m.Source), m.Confidence, m.Importance, string(tags), string(metadata), nullableTask(m.TaskID), nullableSession(m.SessionID), nullableMemory(m.SupersedesID), formatTime(m.CreatedAt), formatTime(m.UpdatedAt), nullableTime(m.ExpiresAt), nullableTime(m.ArchivedAt)}
}
func nullableTask(v *domain.TaskID) any {
	if v == nil {
		return nil
	}
	return string(*v)
}
func nullableSession(v *domain.SessionID) any {
	if v == nil {
		return nil
	}
	return string(*v)
}
func nullableMemory(v *domain.MemoryID) any {
	if v == nil {
		return nil
	}
	return string(*v)
}
func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return formatTime(*v)
}

type rowScanner interface{ Scan(...any) error }

func scanMemory(row rowScanner) (domain.Memory, error) {
	var id, scope, scopeID, kind, content, summary, source, tagsJSON, metadataJSON, createdAt, updatedAt string
	var confidence float64
	var importance int
	var taskID, sessionID, supersedes, expires, archived sql.NullString
	if err := row.Scan(&id, &scope, &scopeID, &kind, &content, &summary, &source, &confidence, &importance, &tagsJSON, &metadataJSON, &taskID, &sessionID, &supersedes, &createdAt, &updatedAt, &expires, &archived); err != nil {
		return domain.Memory{}, err
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return domain.Memory{}, err
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return domain.Memory{}, err
	}
	var tags []string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		return domain.Memory{}, fmt.Errorf("decode tags: %w", err)
	}
	metadata := json.RawMessage(metadataJSON)
	m := domain.Memory{ID: domain.MemoryID(id), Scope: domain.MemoryScope(scope), ScopeID: scopeID, Type: domain.MemoryType(kind), Content: content, Summary: summary, Source: domain.MemorySource(source), Confidence: confidence, Importance: importance, Tags: tags, Metadata: metadata, CreatedAt: created, UpdatedAt: updated}
	if taskID.Valid {
		v := domain.TaskID(taskID.String)
		m.TaskID = &v
	}
	if sessionID.Valid {
		v := domain.SessionID(sessionID.String)
		m.SessionID = &v
	}
	if supersedes.Valid {
		v := domain.MemoryID(supersedes.String)
		m.SupersedesID = &v
	}
	if expires.Valid {
		v, err := parseTime(expires.String)
		if err != nil {
			return domain.Memory{}, err
		}
		m.ExpiresAt = &v
	}
	if archived.Valid {
		v, err := parseTime(archived.String)
		if err != nil {
			return domain.Memory{}, err
		}
		m.ArchivedAt = &v
	}
	if err := m.Validate(); err != nil {
		return domain.Memory{}, fmt.Errorf("validate stored memory: %w", err)
	}
	return m, nil
}
func memoryTerms(query string) []string {
	seen := map[string]struct{}{}
	var terms []string
	for _, item := range strings.Fields(strings.ToLower(query)) {
		item = strings.Trim(item, ".,:;!?()[]{}\"'")
		if item == "" {
			continue
		}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			terms = append(terms, item)
		}
	}
	return terms
}
