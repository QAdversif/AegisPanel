// SPDX-License-Identifier: AGPL-3.0-or-later
//
// aegis-pg-restore — operator-side restore CLI.
// The binary is intentionally SEPARATE from
// `aegis-pg-backup`: the two operations have
// different safety profiles, and putting them
// in different binaries enforces the safety
// boundary at the process level. An operator
// who types `aegis-pg-backup restore <id>` gets
// a `unknown subcommand` error, not a silent
// data wipe.
//
// Usage:
//
//   aegis-pg-restore <id> [--yes] [--dry-run]
//
// Behaviour:
//
//   * Refuses to run unless the panel's restore
//     flag is set (`AEGIS_BACKUPS_ALLOW_UI_RESTORE=true`).
//     The same flag the HTTP handler checks; the
//     CLI is the canonical "intentional" path so
//     the flag is a sanity check, not a security
//     boundary (the DSN is the security boundary).
//   * Refuses to run without `--yes`. The restore
//     drops and recreates every object in the
//     target database; this is destructive. The
//     flag is the operator's affirmative consent.
//   * `--dry-run` prints the SQL plan pg_restore
//     would apply (via `pg_restore --list`) and
//     exits 0 without touching the database.
//
// Environment:
//
//   AEGIS_BACKUPS_DIR             required (default ./var/backups)
//   AEGIS_POSTGRES_DSN            required (the DB to restore INTO)
//   AEGIS_BACKUPS_ALLOW_UI_RESTORE required (must be `true`)

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/QAdversif/AegisPanel/internal/backups"
)

var (
	errUsage         = errors.New("usage error")
	errRefused       = errors.New("refused")
	errDoubleConfirm = errors.New("not confirmed")
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		switch {
		case errors.Is(err, errUsage):
			os.Exit(2)
		case errors.Is(err, errRefused):
			// The message is already on stderr
			// from the call site. Exit 1 — a
			// "refused" run is a non-zero
			// outcome (so the operator's
			// wrapper sees it), but a
			// distinct one from "the
			// pg_restore subprocess
			// crashed".
			os.Exit(1)
		case errors.Is(err, errDoubleConfirm):
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, `{"error":%q}`+"\n", err.Error())
			os.Exit(1)
		}
	}
}

func dispatch(cmd string, args []string) error {
	if cmd == "--help" || cmd == "-h" {
		usage()
		return nil
	}
	// The binary takes the backup id as a
	// positional argument. Subcommands are
	// avoided: aegis-pg-restore is single-purpose,
	// and `aegis-pg-restore <id> --yes` is the
	// entire CLI surface.
	id := cmd
	if strings.HasPrefix(id, "-") {
		usage()
		return errUsage
	}
	yes := false
	dryRun := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes", "-y":
			yes = true
		case "--dry-run":
			dryRun = true
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return runRestore(id, yes, dryRun)
}

func runRestore(id string, yes, dryRun bool) error {
	dsn := os.Getenv("AEGIS_POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("AEGIS_POSTGRES_DSN is required (the database to restore INTO)")
	}
	if !dryRun && !yes {
		// Two-step confirmation. The
		// operator types the id again —
		// a typo (e.g. wrong backup id
		// pasted from the wrong shell
		// history) aborts the restore.
		fmt.Fprintf(os.Stderr, "About to DROP and recreate the database in:\n  %s\nfrom backup %q.\n", redactDSN(dsn), id)
		fmt.Fprint(os.Stderr, "Type the backup id again to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		confirm, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read confirm: %w", err)
		}
		confirm = strings.TrimRight(confirm, "\r\n")
		if confirm != id {
			return fmtDoubleConfirm()
		}
	}
	dir := os.Getenv("AEGIS_BACKUPS_DIR")
	if dir == "" {
		dir = "./var/backups"
	}
	backend, err := backups.NewOSBackend(dir)
	if err != nil {
		return fmt.Errorf("backups: open dir %q: %w", dir, err)
	}
	store := backups.NewLocalStore(backend)
	svc := backups.New(backups.Config{BackupsDir: dir}, store, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if dryRun {
		row, err := svc.Get(ctx, id)
		if err != nil {
			if errors.Is(err, backups.ErrNotFound) {
				return fmt.Errorf("backup %q not found", id)
			}
			return err
		}
		// List the dump file's TOC via
		// `pg_restore --list` and print it.
		// Operators get a quick eyeball check
		// before committing to the
		// destructive op.
		dumpPath := filepath.Join(dir, row.Path)
		// row.Path is server-side (the
		// backups.NewOSBackend joined
		// `bck_..._...` to the operator-
		// controlled dir). The G204 taint
		// does not apply: the path is not
		// user-controllable. The actual
		// injection surface would be a
		// future `--data-only` or
		// `--table=` flag, neither of
		// which this CLI exposes.
		cmd := exec.CommandContext(ctx, "pg_restore", "--list", dumpPath) // #nosec G204,G702 -- row.Path is server-generated
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pg_restore --list: %w", err)
		}
		return nil
	}

	// The Service.Restore method already
	// checks AEGIS_BACKUPS_ALLOW_UI_RESTORE on
	// the server side (via the HTTP handler).
	// The CLI is the operator's intentional
	// path; we still check the env var as a
	// sanity check so a typo in the operator's
	// `EnvironmentFile` doesn't silently
	// disable the safety flag.
	if os.Getenv("AEGIS_BACKUPS_ALLOW_UI_RESTORE") != "true" {
		return fmt.Errorf("%w: AEGIS_BACKUPS_ALLOW_UI_RESTORE must be set to `true` on the host (the DSN is the security boundary — this flag is a sanity check that the operator intended the restore)", errRefused)
	}
	if err := svc.Restore(ctx, id); err != nil {
		if errors.Is(err, backups.ErrNotFound) {
			return fmt.Errorf("backup %q not found", id)
		}
		if errors.Is(err, backups.ErrBackupDisabled) {
			// errors.Join preserves both
			// the wrapper (errRefused, for
			// the dispatch exit-code
			// branch) and the original
			// error (for `errors.Is` /
			// `errors.As` introspection
			// up the stack). Go 1.20+.
			return errors.Join(errRefused, err)
		}
		return err
	}
	// Emit a one-line JSON record so the
	// operator's wrapper script can log
	// success or pipe into audit tooling.
	out := map[string]any{
		"ok":         true,
		"id":         id,
		"restoredAt": time.Now().UTC().Format(time.RFC3339),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// errDoubleConfirm wraps errRefused with a more
// specific cause: the operator typed the wrong
// id. Surfaced as exit 1 with a stderr message
// in dispatch.
func fmtDoubleConfirm() error {
	return fmt.Errorf("%w: typed id did not match", errDoubleConfirm)
}

// redactDSN strips the password from a
// postgres:// DSN for safe display. The format
// is `postgres://user:pass@host:port/db?params`;
// we replace the `pass@` substring with `***@`.
func redactDSN(dsn string) string {
	at := strings.Index(dsn, "@")
	if at < 0 {
		return dsn
	}
	scheme := strings.Index(dsn, "://")
	if scheme < 0 {
		return dsn
	}
	colon := strings.Index(dsn[scheme+3:], ":")
	if colon < 0 {
		return dsn
	}
	colon += scheme + 3
	if colon >= at {
		return dsn
	}
	return dsn[:colon+1] + "***" + dsn[at:]
}

func usage() {
	fmt.Fprintf(os.Stderr, `aegis-pg-restore — operator-side restore CLI

Usage:
  aegis-pg-restore <id> [--yes] [--dry-run]

  --yes, -y     skip the two-step id confirmation
                 (required for non-interactive use)
  --dry-run     print the pg_restore --list TOC and exit

Environment:
  AEGIS_BACKUPS_DIR               (default: ./var/backups)
  AEGIS_POSTGRES_DSN              (required — the DB to restore INTO)
  AEGIS_BACKUPS_ALLOW_UI_RESTORE  (required: must be the literal "true")

The restore drops and recreates every object in the
target database. The DSN is the security boundary;
AEGIS_BACKUPS_ALLOW_UI_RESTORE is a sanity check
that the operator intended the restore (i.e. the
flag was set in the host's EnvironmentFile before
the binary was invoked).

Examples:
  # Dry run: see the SQL plan
  aegis-pg-restore bck_2026_07_28_xxx --dry-run

  # Real run: type the id again when prompted
  aegis-pg-restore bck_2026_07_28_xxx

  # Real run, non-interactive (e.g. disaster recovery drill)
  aegis-pg-restore bck_2026_07_28_xxx --yes
`)
}
