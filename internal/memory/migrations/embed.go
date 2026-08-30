package migrations

import "embed"

// FS holds the SQL migrations for the memory database. They are applied
// in lexical order and must stay idempotent (CREATE ... IF NOT EXISTS),
// matching the convention in the top-level migrations package.
//
//go:embed *.sql
var FS embed.FS
