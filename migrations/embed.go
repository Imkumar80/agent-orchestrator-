// Package migrations provides the versioned SQL migrations used by storage.
package migrations

import "embed"

// Files contains the ordered SQL migration files in this directory.
//
//go:embed *.sql
var Files embed.FS
