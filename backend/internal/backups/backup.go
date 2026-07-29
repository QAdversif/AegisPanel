// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Types: Backup, Trigger, Status. The HTTP wire
// format is the JSON-tagged view of these structs.
// The Store stores the same fields, with the addition
// of the on-disk path (relative to the backups root)
// and a checksum.

package backups

import "time"

// Trigger records who or what initiated the backup.
//
//   - TriggerManual: an operator clicked "Create" in
//     the UI, hit POST /api/v1/backups with
//     {"trigger":"manual"}, or ran `aegis-pg-backup
//     create` on the host.
//   - TriggerScheduled: the in-process scheduler
//     fired on cron match. The Service attaches a
//     `scheduled_at` timestamp from the scheduler
//     clock so audit-log forensics can distinguish
//     a scheduled run from a manual run that happened
//     to overlap.
type Trigger string

const (
	// TriggerManual marks a backup the operator
	// triggered directly: a UI click, a
	// POST /api/v1/backups with
	// {"trigger":"manual"}, or a future
	// `aegis-pg-backup create` invocation.
	TriggerManual Trigger = "manual"
	// TriggerScheduled marks a backup the
	// in-process scheduler fired on cron match.
	TriggerScheduled Trigger = "scheduled"
)

// Status tracks the lifecycle of a single backup.
// The state machine is:
//
//	""      -> creating the row
//	running -> ok     (Success path)
//	running -> failed (Error path; the row is
//	                   retained for forensics with
//	                   `Error` populated, and the
//	                   file (if any partial was
//	                   written) is deleted)
//
// The empty string is only valid for an instant
// between Insert and the first Update; in practice
// the Store inserts with Status="running" so a
// crash mid-backup leaves a clearly-marked
// incomplete row that the operator can delete
// manually.
type Status string

const (
	// StatusRunning marks a backup whose
	// pg_dump subprocess is in flight. The row
	// is created with this status before the
	// subprocess is started; a successful run
	// transitions to StatusOK, a failure to
	// StatusFailed.
	StatusRunning Status = "running"
	// StatusOK marks a backup that completed
	// successfully: the dump file is on disk,
	// the SHA-256 sidecar is written, and the
	// row carries the size + checksum.
	StatusOK Status = "ok"
	// StatusFailed marks a backup whose
	// pg_dump subprocess exited with a non-zero
	// status. The row is retained (with the
	// error string in `Error`) so the operator
	// can audit the failure in the UI; the
	// partial dump file is removed.
	StatusFailed Status = "failed"
)

// Backup is the canonical record for one backup
// file. The `ID` is a ULID-like timestamp+random
// string (`bck_<14-char base32-of-unixtime>_<8-char
// random>`) so the listing in the UI is
// chronologically ordered by default.
//
// The `SizeBytes` is set after pg_dump finishes; the
// Service updates the row before marking Status=ok.
// `ChecksumSHA256` is the hex SHA-256 of the dump
// file. `SchemaVersion` is the highest migration
// number applied at backup time (read from
// `schema_migrations`).
//
// `NodeCount` / `UserCount` / `HostCount` are
// best-effort counts taken AT BACKUP TIME. They
// are NOT updated if the data changes later; the
// purpose is to give the operator a "is this
// backup likely worth keeping" signal at a glance.
//
// `Path` is the relative path of the dump file
// (relative to the backups root). It is included
// in the JSON output because the UI uses it as the
// download filename (`<id>.dump.gz`); it is the
// same value the user already sees in the `id`
// field, so there is no information leak. The
// index file is the only consumer that needs the
// path server-side (Delete uses it to find the
// file on disk).
type Backup struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	SizeBytes      int64     `json:"size_bytes"`
	Trigger        Trigger   `json:"trigger"`
	Status         Status    `json:"status"`
	Error          string    `json:"error,omitempty"`
	SchemaVersion  int       `json:"schema_version"`
	NodeCount      int       `json:"node_count"`
	UserCount      int       `json:"user_count"`
	HostCount      int       `json:"host_count"`
	ChecksumSHA256 string    `json:"checksum_sha256"`
	Path           string    `json:"path,omitempty"`
}

// SetError stores a human-readable error on the
// backup row. The HTTP layer surfaces it via the
// `error` JSON field when Status=failed. Empty
// error on a failed row is allowed (e.g. the
// Service was killed before it could record the
// error string); the UI shows "backup failed,
// no error message recorded" in that case.
func (b *Backup) SetError(err error) {
	if err == nil {
		b.Error = ""
		return
	}
	b.Error = err.Error()
}
