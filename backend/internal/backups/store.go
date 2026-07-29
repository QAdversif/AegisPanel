// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Store: the persistence boundary for backup rows
// and the underlying dump files. LocalStore is the
// only implementation in v0.5.0; future PRs may add
// S3Store (or any S3-compatible blob store) without
// touching the Service or the HTTP handler.
//
// The Store is a pure CRUD facade over the metadata
// (the `Backups` table or, for LocalStore, a JSON
// index file alongside the dumps). The dump bytes
// themselves are written and read directly by the
// Service via `Open` and the `Backend` interface
// below; the Store does not buffer them.

package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned by Store.Get and Store.Delete
// when no row matches the given ID. The HTTP layer
// maps this to 404.
var ErrNotFound = errors.New("backups: not found")

// ErrAlreadyExists is returned by Store.Put when a
// row with the same ID is already present. This is a
// programmer-error guard: the Service generates IDs
// via timestamp + crypto/rand, so collisions are
// not expected in practice. The HTTP layer maps this
// to 500 (it's not a user-visible input case).
var ErrAlreadyExists = errors.New("backups: already exists")

// Backend is the low-level filesystem (or S3) layer.
// Store delegates file Open/Stat/Rename/Remove
// operations to a Backend; the on-disk format is
// determined by the Backend.
//
// The local implementation (`osBackend`) writes
// `<backupsDir>/<id>.dump.gz` and a sidecar
// `<backupsDir>/<id>.sha256` with the hex digest.
// The metadata is stored in a single
// `<backupsDir>/_index.json` file with one row per
// backup. The index is rewritten on every mutation
// (under a write lock) so a backup can be restored
// by reading the index alone — no separate database
// query is required to know what files exist.
//
// Why a JSON index and not a real database? Backups
// are deliberately orthogonal to the panel's
// Postgres: a restore is exactly the case where the
// panel DB is unavailable. The JSON index is a
// single file that lives next to the dumps and is
// self-describing; restoring the index from a
// partial filesystem (some dump files missing) is
// straightforward.
type Backend interface {
	// Stat returns the file size and modtime at the
	// given path (relative to the Backend's root).
	// Returns an error wrapping os.ErrNotExist if
	// the file is not present.
	Stat(ctx context.Context, relPath string) (size int64, modtime time.Time, err error)

	// Create opens a file for writing at relPath.
	// If the file already exists, it is truncated.
	// The caller MUST close the returned WriteCloser.
	Create(ctx context.Context, relPath string) (io.WriteCloser, error)

	// Open opens a file for reading at relPath. The
	// caller MUST close the returned ReadCloser.
	Open(ctx context.Context, relPath string) (io.ReadCloser, error)

	// Remove deletes the file at relPath. A missing
	// file is NOT an error (idempotent).
	Remove(ctx context.Context, relPath string) error

	// Rename moves a file from oldRel to newRel. A
	// missing source is an error.
	Rename(ctx context.Context, oldRel, newRel string) error

	// List returns every path under root that matches
	// the suffix. Used by the retention Cleanup to
	// reconcile index vs filesystem.
	List(ctx context.Context, suffix string) ([]string, error)
}

// Store is the persistence boundary. Implementations
// are LocalStore (the only one in v0.5.0) and
// (future) S3Store, which would wrap an S3 client
// behind the same interface.
//
// All methods are safe for concurrent use. The
// concrete guarantee is "read-after-write consistency
// within a single process": an Insert that returns
// successfully is immediately visible to subsequent
// Get/List calls on the same Store.
type Store interface {
	// Insert adds a new row. The row's `ID`, `Path`,
	// and `CreatedAt` are populated by the caller
	// (the Service). On success, the row's `Status`
	// is whatever the caller set (typically
	// `running` for a new backup).
	Insert(ctx context.Context, b *Backup) error

	// Update replaces the row identified by b.ID. The
	// `CreatedAt` and `ID` are preserved from the
	// stored row; everything else is overwritten.
	// Returns ErrNotFound if no row matches.
	Update(ctx context.Context, b *Backup) error

	// Get returns the row with the given ID, or
	// ErrNotFound.
	Get(ctx context.Context, id string) (*Backup, error)

	// List returns every row, sorted by CreatedAt
	// descending (newest first). The returned slice
	// is freshly allocated.
	List(ctx context.Context) ([]*Backup, error)

	// Delete removes the row identified by id. A
	// missing row is NOT an error (idempotent). The
	// associated dump file is NOT deleted by the
	// Store; the Service does that explicitly so the
	// operator can audit the removal in the same log
	// line as the row deletion.
	Delete(ctx context.Context, id string) error
}

// LocalStore is the v0.5.0 Store. It persists both
// the row metadata (a single JSON index file) and
// the dump bytes (the file at `Path`) via a Backend.
//
// The index file is a `[]Backup` JSON. The slice is
// re-sorted by CreatedAt ascending on every write so
// the on-disk format is canonical regardless of the
// order in which rows were inserted.
type LocalStore struct {
	backend Backend
	mu      sync.Mutex // guards the index file
}

// NewLocalStore returns a Store backed by the given
// Backend. The Backend is expected to be rooted at
// the backups directory.
func NewLocalStore(b Backend) *LocalStore {
	return &LocalStore{backend: b}
}

// indexFile is the on-disk path of the JSON index.
const indexFile = "_index.json"

func (s *LocalStore) readIndex(ctx context.Context) ([]*Backup, error) {
	r, err := s.backend.Open(ctx, indexFile)
	if err != nil {
		if isNotExist(err) {
			return nil, nil // empty index
		}
		return nil, err
	}
	defer closeQuiet(r)
	var out []*Backup
	if err := decodeJSON(r, &out); err != nil {
		return nil, fmt.Errorf("backups: decode index: %w", err)
	}
	return out, nil
}

func (s *LocalStore) writeIndex(ctx context.Context, rows []*Backup) error {
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	w, err := s.backend.Create(ctx, indexFile)
	if err != nil {
		return err
	}
	if err := encodeJSON(w, rows); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// Insert stores the row.
func (s *LocalStore) Insert(ctx context.Context, b *Backup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.ID == b.ID {
			return ErrAlreadyExists
		}
	}
	rows = append(rows, b)
	return s.writeIndex(ctx, rows)
}

// Update replaces the row.
func (s *LocalStore) Update(ctx context.Context, b *Backup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	for i, r := range rows {
		if r.ID == b.ID {
			rows[i] = b
			return s.writeIndex(ctx, rows)
		}
	}
	return ErrNotFound
}

// Get returns a single row.
func (s *LocalStore) Get(ctx context.Context, id string) (*Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.ID == id {
			// Return a copy so the caller can mutate
			// without affecting the in-memory cache.
			cp := *r
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

// List returns every row, newest first.
func (s *LocalStore) List(ctx context.Context) ([]*Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	out := make([]*Backup, len(rows))
	for i, r := range rows {
		cp := *r
		out[i] = &cp
	}
	return out, nil
}

// Delete removes a row. The associated dump file is
// not touched.
func (s *LocalStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	out := rows[:0]
	found := false
	for _, r := range rows {
		if r.ID == id {
			found = true
			continue
		}
		out = append(out, r)
	}
	if !found {
		return nil // idempotent
	}
	return s.writeIndex(ctx, out)
}

// osBackend is the default Backend, rooted at a
// local directory. All operations are relative to
// `root`; the path "..", absolute paths, and
// backslashes are rejected to keep this layer's
// safety guarantees identical to the S3 future
// implementation (where a similar escape would
// denote "leak the bucket prefix").
type osBackend struct {
	root string
}

// NewOSBackend returns the default Backend rooted at
// `root`. The directory is created if it does not
// exist; the caller is responsible for setting
// ownership and mode (chmod 0700, owner
// aegis-deploy) before instantiating.
func NewOSBackend(root string) (Backend, error) {
	if root == "" {
		return nil, errors.New("backups: empty root path")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("backups: mkdir %s: %w", root, err)
	}
	return &osBackend{root: root}, nil
}

func (b *osBackend) resolve(relPath string) (string, error) {
	if relPath == "" {
		return "", errors.New("backups: empty path")
	}
	if strings.Contains(relPath, "..") || strings.HasPrefix(relPath, "/") || strings.Contains(relPath, "\\") {
		return "", fmt.Errorf("backups: invalid path %q", relPath)
	}
	return filepath.Join(b.root, relPath), nil
}

func (b *osBackend) Stat(ctx context.Context, relPath string) (int64, time.Time, error) {
	p, err := b.resolve(relPath)
	if err != nil {
		return 0, time.Time{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return 0, time.Time{}, err
	}
	return st.Size(), st.ModTime(), nil
}

func (b *osBackend) Create(ctx context.Context, relPath string) (io.WriteCloser, error) {
	p, err := b.resolve(relPath)
	if err != nil {
		return nil, err
	}
	// `p` is the join of `b.root` and `relPath`
	// after `b.resolve` has rejected `..`,
	// absolute paths, and backslashes; the
	// remaining injection surface is empty.
	return os.Create(p) // #nosec G703 -- resolve() rejects traversal
}

func (b *osBackend) Open(ctx context.Context, relPath string) (io.ReadCloser, error) {
	p, err := b.resolve(relPath)
	if err != nil {
		return nil, err
	}
	// `p` is the join of `b.root` and `relPath`
	// after `b.resolve` has rejected `..`,
	// absolute paths, and backslashes; the
	// remaining injection surface is empty.
	return os.Open(p) // #nosec G703 -- resolve() rejects traversal
}

func (b *osBackend) Remove(ctx context.Context, relPath string) error {
	p, err := b.resolve(relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (b *osBackend) Rename(ctx context.Context, oldRel, newRel string) error {
	oldP, err := b.resolve(oldRel)
	if err != nil {
		return err
	}
	newP, err := b.resolve(newRel)
	if err != nil {
		return err
	}
	return os.Rename(oldP, newP)
}

func (b *osBackend) List(ctx context.Context, suffix string) ([]string, error) {
	entries, err := os.ReadDir(b.root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// isNotExist is a small helper that normalises
// "file not found" errors across the stdlib and
// the various wrappers we encounter.
func isNotExist(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, os.ErrNotExist)
}
