CREATE TABLE daemon_lease (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    owner_id TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
