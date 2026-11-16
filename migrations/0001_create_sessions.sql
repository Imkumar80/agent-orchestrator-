CREATE TABLE sessions (
    id TEXT PRIMARY KEY NOT NULL,
    task_id TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('codex', 'opencode', 'hermes', 'openclaw')),
    native_provider_session_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN (
        'pending', 'starting', 'running', 'waiting_for_input',
        'completed', 'failed', 'interrupted'
    )),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL CHECK (updated_at >= created_at),
    error_code TEXT,
    error_message TEXT,
    CHECK (
        (error_code IS NULL AND error_message IS NULL) OR
        (error_code IS NOT NULL AND error_message IS NOT NULL)
    )
);
