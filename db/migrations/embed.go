// Package migrations embeds the goose migrations into the binary (migrate role).
//
// Rules: expand/contract only: a migration must not break running instances of the previous version
// (add the column/table first, switch the code, drop it in the next release). Every file has Up and Down.
package migrations

import "embed"

// FS holds all migrations (NNNNN_name.sql).
//
//go:embed *.sql
var FS embed.FS
