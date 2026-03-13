package migrations

import "embed"

// FS stores the SQL migrations shipped with the agent.
//
//go:embed *.sql
var FS embed.FS
