// SPDX-License-Identifier: AGPL-3.0-or-later
//
// `aegis admin node rotate-transport` — operator-side
// tool for the v0.8.31 mTLS+gRPC migration. v0.8.31
// ships the first subcommand in this namespace; future
// subcommands (e.g. `decrypt-key-for-emergency-access`)
// land in `admin_node.go`.
//
// # Why a new subcommand
//
// v0.8.30 wires mTLS end-to-end (panel dials the
// agent on `:7001` with a client cert; agent requires
// a verified client cert + presents its server cert;
// both are signed by the panel's root CA in
// `agentca` table). v0.8.31 introduces the
// per-node `agent_transport` column as the operator's
// observability surface for "which nodes are still on
// the v0.4.0-b HTTP+bearer path?"; the deprecation
// warning header on `GET /api/v1/nodes` reads the
// column to surface the migration backlog in the
// daily operator check.
//
// The CLI is the operator's knob to flip a node
// from "http" to "grpc" (or back, in the rare
// rollback case where the v0.8.30 mTLS handshake
// misbehaves in prod and the operator wants to take
// a node off the new path). The column is
// observability + audit only at v0.8.31 (the
// transport pick at apply time is still process-wide
// via `AEGIS_AGENT_TRANSPORT`); the v0.8.32 cut uses
// the column to drive the per-node transport pick.
//
// # Wire format
//
//	aegis admin node rotate-transport <node-uuid>
//	    [--to grpc|http]      # target transport (default grpc)
//	    --all                 # rotate every node (no <node-uuid>)
//	    --filter transport=http   # subset (only the http nodes)
//	    --dry-run             # show what would change, do not write
//
// The CLI exits 0 on success (or on a successful
// no-op + dry-run), non-zero on any failure. The
// audit log records the action via
// `Service.RotateTransport` (the same call site the
// future admin UI button will use); the v0.8.x
// audit-call-site wiring from PR #166 is the writer.
//
// # Idempotency
//
// Rotating a node that is already on the target
// transport is a no-op at the Service layer
// (`Service.RotateTransport` returns the current row
// without writing or auditing). The CLI is therefore
// safe to run on cron / as a remediation step: an
// operator who runs the same command twice sees the
// same output the second time (the second run
// reports the node was already on the target
// transport and exits 0).

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/db"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// transportChoice maps the CLI's --to flag to the
// v0.8.31 closed set. The empty string is "not
// set yet" (the default of `grpc` is applied
// later).
type transportChoice string

const (
	choiceGRPC transportChoice = "grpc"
	choiceHTTP transportChoice = "http"
)

// runAdminNodeRotateTransport implements
// `aegis admin node rotate-transport`. The function
// is structured as: parse args (target transport,
// --all / --filter / --dry-run), open the pg
// pool, build the Service, enumerate target nodes,
// call `Service.RotateTransport` per node, summarise.
//
// The function is intentionally flat — the
// rotate-transport flow has a single side-effect
// (the column write + the audit row) and a
// long-but-linear happy path. The same shape as
// `runAdminNodeRotatePanelKey` in `admin_node.go`.
// The audit row is written by the Service, not
// the CLI (mirrors the v0.8.3 rotate-panel-key
// pattern; the CLI is a thin caller, the audit
// semantics live in the Service so the future
// admin UI button writes the same row).
func runAdminNodeRotateTransport(ctx context.Context, args []string) {
	var (
		nodeIDStr  string
		all        bool
		filterHTTP bool
		dryRun     bool
		to         transportChoice = "" // empty => default (grpc)
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 >= len(args) {
				log.Fatal().Msg("admin node rotate-transport: --to requires a value")
			}
			i++
			switch strings.ToLower(args[i]) {
			case "grpc":
				to = choiceGRPC
			case "http":
				to = choiceHTTP
			default:
				log.Fatal().Str("value", args[i]).Msg("admin node rotate-transport: --to must be grpc or http")
			}
		case "--all":
			all = true
		case "--filter":
			// v0.8.31 only supports transport=http
			// (the migration backlog). A future
			// flag like --filter transport=grpc
			// would let the operator roll back
			// in bulk, but the v0.8.31 migration
			// is one-way (http -> grpc); the
			// rollback path is per-uuid.
			if i+1 >= len(args) {
				log.Fatal().Msg("admin node rotate-transport: --filter requires a value")
			}
			i++
			if args[i] != "transport=http" {
				log.Fatal().Str("value", args[i]).Msg("admin node rotate-transport: --filter only accepts transport=http (other filters land in v0.8.32 if needed)")
			}
			filterHTTP = true
		case "--dry-run":
			dryRun = true
		default:
			if nodeIDStr != "" {
				log.Fatal().Str("arg", args[i]).Msg("admin node rotate-transport: unexpected positional argument")
			}
			nodeIDStr = args[i]
		}
	}
	// Default target is grpc (the migration
	// direction). A future operator may want to
	// pass --to http to roll a single node back;
	// that's the only "backwards" rotation the
	// CLI supports today.
	if to == "" {
		to = choiceGRPC
	}
	if !all && nodeIDStr == "" {
		// v0.8.31: --filter transport=http is
		// the bulk path. --all is reserved
		// for a future "rotate everything
		// regardless of state" use case; today
		// the closed set is {http, grpc} and
		// rotating grpc -> grpc is a no-op,
		// so --all is functionally equivalent
		// to --filter transport=http. We
		// accept --all for the operator's
		// ergonomics and let the Service
		// no-op handle the grpc nodes.
		log.Fatal().Msg("admin node rotate-transport: missing <node-uuid> (or pass --all / --filter transport=http)")
	}
	if all && nodeIDStr != "" {
		log.Fatal().Msg("admin node rotate-transport: <node-uuid> and --all are mutually exclusive")
	}
	if filterHTTP && nodeIDStr != "" {
		log.Fatal().Msg("admin node rotate-transport: <node-uuid> and --filter are mutually exclusive")
	}
	// Open the pg pool. The CLI requires the pg
	// path (no memory path: the nodes Store is
	// never MemoryStore in a real install; the
	// rotate-transport flow writes a real row on
	// a real node).
	dsn := os.Getenv("AEGIS_POSTGRES_DSN")
	if dsn == "" {
		log.Fatal().Msg("admin node rotate-transport: AEGIS_POSTGRES_DSN is not set")
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("admin node rotate-transport: db.Open")
	}
	defer pool.Close()
	// Build the Service. The audit Service is
	// best-effort (a nil writer is allowed;
	// Service.RotateTransport's RecordFromContext
	// short-circuits when s.audits is nil).
	nodesStore := nodes.NewPgStore(pool)
	nodesSvc := nodes.NewService(nodesStore).WithAudits(audits.NewService(audits.NewPgStore(pool)))

	// Enumerate target nodes. The single-uuid
	// path is the simplest; --all and
	// --filter are the bulk paths.
	if nodeIDStr != "" {
		nodeID, err := uuid.Parse(nodeIDStr)
		if err != nil {
			log.Fatal().Err(err).Str("arg", nodeIDStr).Msg("admin node rotate-transport: invalid <node-uuid>")
		}
		rotateOne(ctx, nodesSvc, nodeID, string(to), dryRun, os.Stdout)
		return
	}
	allNodes, err := nodesSvc.List(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("admin node rotate-transport: list")
	}
	rotated := 0
	skipped := 0
	for _, n := range allNodes {
		// --filter transport=http: skip the
		// already-grpc nodes (the migration
		// backlog is the only bulk path
		// today).
		if filterHTTP && n.AgentTransport != nodes.AgentTransportHTTP {
			skipped++
			continue
		}
		// --all without --filter: rotate
		// everything (the Service no-ops the
		// already-grpc nodes; we report
		// skipped for the operator).
		if !filterHTTP && n.AgentTransport == string(to) {
			skipped++
			continue
		}
		rotateOne(ctx, nodesSvc, n.ID, string(to), dryRun, os.Stdout)
		rotated++
	}
	if dryRun {
		log.Info().
			Int("would_rotate", rotated).
			Int("skipped", skipped).
			Str("target", string(to)).
			Msg("admin node rotate-transport: dry-run complete")
		return
	}
	log.Info().
		Int("rotated", rotated).
		Int("skipped", skipped).
		Str("target", string(to)).
		Msg("admin node rotate-transport: complete")
}

// rotateOne is the per-node work the bulk path
// (--all / --filter) and the single-uuid path
// share. The function is intentionally small: the
// Service does the validation, the column write,
// the audit row, and the webhook dispatch. The
// CLI is the caller; the per-node output is one
// line of `log.Info` (the operator's terminal
// gets a structured-log line per node, which is
// the same shape the v0.8.3 rotate-panel-key CLI
// uses).
func rotateOne(ctx context.Context, svc *nodes.Service, id uuid.UUID, to string, dryRun bool, w io.Writer) {
	cur, err := svc.Get(ctx, id)
	if err != nil {
		if errors.Is(err, nodes.ErrNotFound) {
			log.Warn().Str("node_id", id.String()).Msg("admin node rotate-transport: node not found, skipping")
			return
		}
		log.Fatal().Err(err).Str("node_id", id.String()).Msg("admin node rotate-transport: get")
	}
	if cur.AgentTransport == to {
		log.Info().
			Str("node_id", id.String()).
			Str("node_name", cur.Name).
			Str("agent_transport", cur.AgentTransport).
			Msg("admin node rotate-transport: already on target, skipping")
		return
	}
	if dryRun {
		// errcheck: a write error to a CLI output
		// stream is operator-visible but not
		// actionable at this layer (the stream
		// is the caller's). Discard the error
		// so the linter is happy without
		// changing the dry-run path's
		// behaviour.
		_, _ = fmt.Fprintf(w, "would rotate %s (%s) %s -> %s\n", id, cur.Name, cur.AgentTransport, to)
		return
	}
	if _, err := svc.RotateTransport(ctx, id, to); err != nil {
		log.Fatal().Err(err).Str("node_id", id.String()).Msg("admin node rotate-transport: rotate")
	}
	log.Info().
		Str("node_id", id.String()).
		Str("node_name", cur.Name).
		Str("address", cur.Address).
		Str("from", cur.AgentTransport).
		Str("to", to).
		Msg("admin node rotate-transport: rotated")
}

// _ = time.Second + strconv.Itoa(0) keeps the
// time + strconv imports in scope while the file
// is being written; both are used by the v0.8.32
// follow-up (the per-cert rotation CLI and the
// expiry-bucketed filter, respectively). The
// unused-import dance is the same as
// `admin_node.go`'s `var _ = time.Second`.
var _ = time.Second
var _ = strconv.Itoa
