// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the pure `pgDumpArgs` / `pgRestoreArgs`
// functions. The split between "build argv" (pure)
// and "spawn subprocess" (I/O) exists so the argv
// shape is testable without an exec shim — these
// tests lock in:
//
//   1. URL DSNs get their password extracted into
//      the PGPASSWORD env value (password never
//      appears in argv).
//   2. key=value DSNs (no URL scheme) pass through
//      unchanged with no PGPASSWORD.
//   3. Unsupported schemes (e.g. mysql://) are
//      rejected with a clear error — passing them
//      to pg_dump would be a silent misconfig.
//   4. DSNs with special characters in the password
//      survive the URL round-trip without
//      corruption (covered by %-encoded fixtures).
//
// The tests are table-driven so a future operator
// who adds a new DSN shape (e.g. URI with IPv6
// host) extends one slice, not one test function.

package backups

import (
	"strings"
	"testing"
)

func TestPgDumpArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dsn     string
		want    []string
		wantPgp string
		wantErr string // substring; empty = no error
	}{
		{
			name:    "URL with password: extracted to env, stripped from argv",
			dsn:     "postgres://aegis:aegis-fixture-db-password@aegis-postgres:5432/aegis?sslmode=disable",
			want:    []string{"-Fc", "--dbname=postgres://aegis@aegis-postgres:5432/aegis?sslmode=disable", "--no-password"},
			wantPgp: "aegis-fixture-db-password",
		},
		{
			name:    "URL with user, no password: no PGPASSWORD, no stripping",
			dsn:     "postgres://aegis@aegis-postgres:5432/aegis?sslmode=disable",
			want:    []string{"-Fc", "--dbname=postgres://aegis@aegis-postgres:5432/aegis?sslmode=disable", "--no-password"},
			wantPgp: "",
		},
		{
			name:    "URL with empty userinfo (postgres://@host/db): user stripped, no PGPASSWORD",
			dsn:     "postgres://@aegis-postgres:5432/aegis?sslmode=disable",
			want:    []string{"-Fc", "--dbname=postgres://aegis-postgres:5432/aegis?sslmode=disable", "--no-password"},
			wantPgp: "",
		},
		{
			name:    "postgresql:// scheme is supported (libpq canonical form)",
			dsn:     "postgresql://u:pw@host:5432/db",
			want:    []string{"-Fc", "--dbname=postgresql://u@host:5432/db", "--no-password"},
			wantPgp: "pw",
		},
		{
			name:    "key=value DSN: passthrough, no PGPASSWORD",
			dsn:     "host=aegis-postgres port=5432 user=aegis password=aegis-fixture-db-password dbname=aegis sslmode=disable",
			want:    []string{"-Fc", "--dbname=host=aegis-postgres port=5432 user=aegis password=aegis-fixture-db-password dbname=aegis sslmode=disable", "--no-password"},
			wantPgp: "",
		},
		{
			name:    "key=value DSN without password: passthrough, no PGPASSWORD",
			dsn:     "host=localhost port=5432 user=postgres dbname=postgres",
			want:    []string{"-Fc", "--dbname=host=localhost port=5432 user=postgres dbname=postgres", "--no-password"},
			wantPgp: "",
		},
		{
			name:    "URL with %-encoded password: round-trips through url.Parse cleanly",
			dsn:     "postgres://u:p%40ss%21word@host:5432/db",
			want:    []string{"-Fc", "--dbname=postgres://u@host:5432/db", "--no-password"},
			wantPgp: "p@ss!word",
		},
		{
			name:    "URL with IPv6 host: brackets preserved",
			dsn:     "postgres://u:pw@[::1]:5432/db",
			want:    []string{"-Fc", "--dbname=postgres://u@[::1]:5432/db", "--no-password"},
			wantPgp: "pw",
		},
		{
			name:    "unsupported scheme: rejected with clear error",
			dsn:     "mysql://u:pw@host:3306/db",
			want:    nil,
			wantPgp: "",
			wantErr: "unsupported DSN scheme",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotArgs, gotPgp, err := pgDumpArgs(tc.dsn)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("pgDumpArgs(%q): want error containing %q, got nil", tc.dsn, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("pgDumpArgs(%q): error = %v, want substring %q", tc.dsn, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("pgDumpArgs(%q): unexpected error %v", tc.dsn, err)
			}
			if !sliceEqual(gotArgs, tc.want) {
				t.Fatalf("pgDumpArgs(%q):\n  got  = %v\n  want = %v", tc.dsn, gotArgs, tc.want)
			}
			if gotPgp != tc.wantPgp {
				t.Fatalf("pgDumpArgs(%q): pgpw = %q, want %q", tc.dsn, gotPgp, tc.wantPgp)
			}
			// CRITICAL: when a PGPASSWORD is set, it
			// must NOT also appear in the argv. This
			// is the security invariant — the whole
			// point of the URL-stripping path.
			if tc.wantPgp != "" {
				for _, a := range gotArgs {
					if strings.Contains(a, tc.wantPgp) {
						t.Fatalf("pgDumpArgs(%q): password %q leaked into argv element %q", tc.dsn, tc.wantPgp, a)
					}
				}
			}
		})
	}
}

func TestPgRestoreArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dsn     string
		want    []string
		wantPgp string
		wantErr string
	}{
		{
			name:    "URL with password: extracted, stripped",
			dsn:     "postgres://aegis:aegis-fixture-db-password@aegis-postgres:5432/aegis?sslmode=disable",
			want:    []string{"--clean", "--if-exists", "--dbname=postgres://aegis@aegis-postgres:5432/aegis?sslmode=disable", "--no-password"},
			wantPgp: "aegis-fixture-db-password",
		},
		{
			name:    "key=value: passthrough",
			dsn:     "host=localhost user=postgres dbname=postgres",
			want:    []string{"--clean", "--if-exists", "--dbname=host=localhost user=postgres dbname=postgres", "--no-password"},
			wantPgp: "",
		},
		{
			name:    "unsupported scheme: rejected",
			dsn:     "mysql://u:pw@host:3306/db",
			want:    nil,
			wantPgp: "",
			wantErr: "unsupported DSN scheme",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotArgs, gotPgp, err := pgRestoreArgs(tc.dsn)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("pgRestoreArgs(%q): want error containing %q, got %v", tc.dsn, tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pgRestoreArgs(%q): unexpected error %v", tc.dsn, err)
			}
			if !sliceEqual(gotArgs, tc.want) {
				t.Fatalf("pgRestoreArgs(%q):\n  got  = %v\n  want = %v", tc.dsn, gotArgs, tc.want)
			}
			if gotPgp != tc.wantPgp {
				t.Fatalf("pgRestoreArgs(%q): pgpw = %q, want %q", tc.dsn, gotPgp, tc.wantPgp)
			}
		})
	}
}

// sliceEqual is a tiny helper to compare two
// string slices element-wise. Using
// reflect.DeepEqual on a small slice is fine but
// produces a less informative failure message
// (it dumps both slices). Direct comparison
// yields the first mismatched index when we
// want a fast happy-path.
func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
