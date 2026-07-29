// SPDX-License-Identifier: AGPL-3.0-or-later
//
// aegis-pg-backup — operator-side CLI for the v0.5.0
// backup surface. The binary is the canonical
// cron-friendly entry point: it bypasses the panel's
// HTTP surface and calls the `backups.Service`
// directly. The HTTP endpoint `POST
// /api/v1/backups/` is for the UI; the CLI is for
// the operator's own scheduler (`crontab`,
// systemd-timer, etc.).
//
// Subcommands:
//
//   aegis-pg-backup list                       — print all rows as JSON
//   aegis-pg-backup get <id>                  — print one row as JSON
//   aegis-pg-backup create [--trigger ...]    — run a new backup, print row
//   aegis-pg-backup delete <id>               — drop the row + dump file
//   aegis-pg-backup download <id> <path>      — write the .dump.gz to <path>
//
// Environment:
//
//   AEGIS_BACKUPS_DIR       required for every subcommand
//                          (default ./var/backups for dev)
//   AEGIS_POSTGRES_DSN      required for `create`
//                          (passed to pg_dump -d)
//
// Output: every subcommand writes a single JSON
// value to stdout and exits 0. Errors go to stderr
// in `{"error":"<message>"}` shape with a non-zero
// exit code. The JSON-to-stdout contract is what
// makes the binary cron-friendly: `aegis-pg-backup
// list | jq -r '.[].id'` is the canonical way to
// feed a downstream pipe.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/QAdversif/AegisPanel/internal/backups"
)

// errUsage is returned when the operator's
// invocation is malformed (wrong subcommand,
// missing args, etc.). The exit code for
// `errUsage` is 2 — the conventional "usage
// error" code in shell.
var errUsage = errors.New("usage error")

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		if errors.Is(err, errUsage) {
			// errUsage already printed usage +
			// a one-line explanation. Exit 2.
			os.Exit(2)
		}
		// Surface a structured JSON error to
		// stderr. Cron wrappers that pipe
		// stdout into `jq` do not see this; the
		// exit code is the canonical "this
		// command failed" signal.
		fmt.Fprintf(os.Stderr, `{"error":%q}`+"\n", err.Error())
		os.Exit(1)
	}
}

// dispatch routes the subcommand. The function
// returns `errUsage` (exit 2) on bad invocation
// and the underlying error (exit 1) on a runtime
// failure.
func dispatch(cmd string, args []string) error {
	// `aegis-pg-backup --help` / `-h` — print usage
	// and exit 0 (the conventional "this CLI is
	// self-documenting" shape).
	if cmd == "--help" || cmd == "-h" {
		usage()
		return nil
	}

	// Open the local store. The directory
	// (`AEGIS_BACKUPS_DIR`) is auto-created at
	// mode 0700 by `backups.NewOSBackend`. We
	// pass `nil` for the pool because the
	// metadata counts (nodes / users / hosts)
	// are best-effort: the CLI does not require
	// Postgres connectivity for list / get /
	// download / delete.
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

	ctx := context.Background()

	switch cmd {
	case "list":
		return runList(ctx, svc)
	case "get":
		if len(args) != 1 {
			usage()
			return errUsage
		}
		return runGet(ctx, svc, args[0])
	case "create":
		return runCreate(ctx, svc, args)
	case "delete":
		if len(args) != 1 {
			usage()
			return errUsage
		}
		return runDelete(ctx, svc, args[0])
	case "download":
		if len(args) != 2 {
			usage()
			return errUsage
		}
		return runDownload(ctx, svc, args[0], args[1])
	default:
		fmt.Fprintf(os.Stderr, "aegis-pg-backup: unknown subcommand %q\n", cmd)
		usage()
		return errUsage
	}
}

func runList(ctx context.Context, svc *backups.Service) error {
	rows, err := svc.List(ctx)
	if err != nil {
		return err
	}
	if rows == nil {
		// Always emit a valid JSON array (never
		// `null`) so downstream jq pipelines
		// work on the empty case.
		rows = []*backups.Backup{}
	}
	return writeJSON(os.Stdout, rows)
}

func runGet(ctx context.Context, svc *backups.Service, id string) error {
	row, err := svc.Get(ctx, id)
	if err != nil {
		if errors.Is(err, backups.ErrNotFound) {
			return fmt.Errorf("backup %q not found", id)
		}
		return err
	}
	return writeJSON(os.Stdout, row)
}

func runCreate(ctx context.Context, svc *backups.Service, args []string) error {
	// Parse the (optional) --trigger flag.
	trigger := backups.TriggerManual
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--trigger":
			if i+1 >= len(args) {
				return fmt.Errorf("--trigger requires a value")
			}
			trigger = backups.Trigger(args[i+1])
			i++
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}
	// `create` is the only subcommand that
	// requires Postgres connectivity (the
	// `pg_dump` subprocess). A missing DSN is a
	// real operator error; the empty string is
	// never a valid value.
	if os.Getenv("AEGIS_POSTGRES_DSN") == "" {
		return fmt.Errorf("AEGIS_POSTGRES_DSN is required for `create`")
	}
	// Inject the DSN into the Service config
	// AFTER `backups.New` (which used the env
	// var set on the host). The Config struct
	// holds the DSN; we re-set it here so the
	// CLI does not have to construct a fresh
	// Service. The pool is still nil — the
	// Service only uses the DSN for the
	// `pg_dump -d` argument, not for the
	// metadata counts.
	// (See Service.Create: it pulls the DSN from
	// s.cfg.PostgresDSN at dump-spawn time.)
	// Reflective access is intentionally
	// avoided: re-create the service with the
	// full config.
	dsn := os.Getenv("AEGIS_POSTGRES_DSN")
	svc = backups.New(backups.Config{
		BackupsDir:  os.Getenv("AEGIS_BACKUPS_DIR"),
		PostgresDSN: dsn,
	}, svc.Store(), nil)

	row, err := svc.Create(ctx, trigger)
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, row)
}

func runDelete(ctx context.Context, svc *backups.Service, id string) error {
	if err := svc.Delete(ctx, id); err != nil {
		if errors.Is(err, backups.ErrNotFound) {
			return fmt.Errorf("backup %q not found", id)
		}
		return err
	}
	return nil
}

func runDownload(ctx context.Context, svc *backups.Service, id, dst string) error {
	// Resolve the destination to an absolute
	// path before opening the file. The operator
	// may pass a relative path (e.g.
	// `./latest.dump.gz`) and we want the
	// `os.Create` to honour the caller's CWD.
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolve destination path: %w", err)
	}
	// Refuse to follow a destination that
	// would land inside the backups dir itself
	// (the dump file is a *target* of the
	// download, not something the operator
	// wants to overwrite with itself). The
	// check is a simple prefix match after
	// filepath.Clean.
	backupsDir, err := filepath.Abs(os.Getenv("AEGIS_BACKUPS_DIR"))
	if err != nil {
		return fmt.Errorf("resolve backups dir: %w", err)
	}
	if strings.HasPrefix(dstAbs, backupsDir+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to download into the backups dir (%s)", backupsDir)
	}

	f, err := svc.Open(ctx, id)
	if err != nil {
		if errors.Is(err, backups.ErrNotFound) {
			return fmt.Errorf("backup %q not found", id)
		}
		return err
	}
	defer f.Close() //nolint:errcheck // read-only dump file, close error is not actionable

	out, err := os.Create(dstAbs)
	if err != nil {
		return err
	}
	// Best-effort close + cleanup on copy
	// failure. The close error is reported if
	// the copy itself succeeded; otherwise the
	// cleanup half-truncates the partial file.
	if _, err := io.Copy(out, f); err != nil {
		_ = out.Close()
		_ = os.Remove(dstAbs)
		return fmt.Errorf("copy dump: %w", err)
	}
	return out.Close()
}

// writeJSON marshals v to w with a trailing
// newline. The trailing newline is what makes
// the output a "line" (so `xargs`, `jq`, and
// shell pipes are happy with the result).
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `aegis-pg-backup — operator CLI for the v0.5.0 backup surface

Usage:
  aegis-pg-backup list
  aegis-pg-backup get <id>
  aegis-pg-backup create [--trigger manual|scheduled]
  aegis-pg-backup delete <id>
  aegis-pg-backup download <id> <path>

Environment:
  AEGIS_BACKUPS_DIR    (default: ./var/backups)
  AEGIS_POSTGRES_DSN   (required for the create subcommand)

Output:
  Every subcommand writes a single JSON value to stdout
  and exits 0. Errors go to stderr in {"error":"..."}
  shape with a non-zero exit code.

Examples:
  # Cron entry — daily dump at 02:00, list rotated
  # to keep a 30-day window.
  0 2 * * *  aegis-pg-backup create >> /var/log/aegis/backup.log 2>&1
  0 3 * * *  aegis-pg-backup list | jq -r '.[] | select(.status == "ok") | .id' \
                  | head -n -30 | xargs -r aegis-pg-backup delete

See docs/operator-guide.md for the full restore
workflow (aegis-pg-restore is a separate binary).
`)
}
