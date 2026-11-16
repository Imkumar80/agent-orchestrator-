CREATE TABLE memories (
    id TEXT PRIMARY KEY NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('global', 'task', 'session')),
    scope_id TEXT NOT NULL DEFAULT '',
    memory_type TEXT NOT NULL CHECK (memory_type IN ('working', 'episodic', 'semantic')),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 32768),
    summary TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('user', 'agent', 'tool', 'system')),
    confidence REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    importance INTEGER NOT NULL CHECK (importance BETWEEN 1 AND 5),
    tags_json TEXT NOT NULL DEFAULT '[]',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    task_id TEXT,
    session_id TEXT REFERENCES sessions(id) ON DELETE RESTRICT,
    supersedes_id TEXT REFERENCES memories(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL CHECK (updated_at >= created_at),
    expires_at TEXT,
    archived_at TEXT,
    CHECK (
        (scope = 'global' AND scope_id = '') OR
        (scope = 'task' AND scope_id <> '' AND task_id = scope_id) OR
        (scope = 'session' AND scope_id <> '' AND session_id = scope_id)
    ),
    CHECK (memory_type <> 'working' OR expires_at IS NOT NULL),
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (archived_at IS NULL OR archived_at >= created_at),
    CHECK (supersedes_id IS NULL OR supersedes_id <> id)
);

CREATE INDEX memories_scope_type_updated_idx
    ON memories(scope, scope_id, memory_type, updated_at DESC);
CREATE INDEX memories_expiry_archive_idx
    ON memories(memory_type, expires_at, archived_at);
CREATE UNIQUE INDEX memories_one_superseder_idx
    ON memories(supersedes_id) WHERE supersedes_id IS NOT NULL;
