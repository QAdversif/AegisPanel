// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Consumer-side interfaces for the backups
// subsystem. The Service uses a Dumper to obtain
// the logical-dump stream and a Restorer to import
// a dump file. Production wiring installs the
// concrete pgBinaries; tests inject fakes via
// Service.SetDumper / Service.SetRestorer.
//
// The split (Dump / Restore as separate interfaces)
// is intentional: the consumer-side interface idiom
// in Go keeps each call site's surface tight. A future
// remote-dump provider (S3, ssh-streamed) only
// implements Dumper; a future logical-replication
// restore only implements Restorer.
//
// Both interfaces are stateless from the Service's
// point of view: the Service holds the DSN in its
// Config, and the DSN is the only piece of state
// that varies per call. The Dumper / Restorer do
// not cache the DSN — they accept it per invocation.

package backups

import (
	"context"
	"io"
)

// Dumper produces a logical-backup stream for the
// postgres database identified by dsn. The returned
// ReadCloser MUST be closed by the caller; the Close
// error is the operation result (e.g. the underlying
// subprocess's exit code). The Service treats a Close
// error as a failed backup and surfaces the error
// message to the operator.
//
// The dsn is the full postgres connection string
// (e.g. `postgres://user:pw@host:5432/db?sslmode=…`).
// Implementations are free to extract just the parts
// they need; pgBinaries, the production impl,
// passes the URL minus its password to pg_dump and
// surfaces the password via the PGPASSWORD env var.
type Dumper interface {
	Dump(ctx context.Context, dsn string) (io.ReadCloser, error)
}

// Restorer imports a logical-backup file at
// dumpPath into the postgres database identified by
// dsn. The operation is destructive (drops and
// recreates the database objects in the dump);
// callers are expected to have stopped the panel
// before invoking. The Service gates the HTTP-level
// Restore endpoint with AllowUIRestore; the CLI
// binary in cmd/aegis-pg-restore bypasses the gate
// by calling Service.Restore directly.
//
// The dsn follows the same shape as in Dumper.
type Restorer interface {
	Restore(ctx context.Context, dsn, dumpPath string) error
}
