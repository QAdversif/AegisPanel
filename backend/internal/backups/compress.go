// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Compression wrapper. The `pg_dump -Fc` custom
// format is already compressed, so the gzip layer
// here is a thin metadata-only overhead. It exists
// because the operator workflow (see
// `deploy/secrets/README.md` §Rotation) expects
// `.dump.gz` extensions and the downstream restore
// pipeline (future PR) pipes through `zcat` for
// stdout inspection. Keeping the layer in the
// panel means a future "skip gzip" config flag
// only touches this file, not the Service.

package backups

import (
	"compress/gzip"
	"io"
)

// newGzipWriterImpl wraps `w` in a gzip writer. The
// caller MUST close the returned WriteCloser to
// flush the gzip footer.
func newGzipWriterImpl(w io.Writer) (io.WriteCloser, error) {
	return gzip.NewWriterLevel(w, gzip.BestSpeed)
}
