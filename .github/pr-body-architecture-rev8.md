# docs(architecture): rev8 — review-driven fixes

## Summary

Comprehensive review-driven update to `ARCHITECTURE.md` after external
AI review (2026-07-17). Brings the document back in sync with the
code state (47 PR merged) and fixes architectural issues identified
during the review + comparison with Remnawave.

## Changes

### Roadmap unification (P0)

- **§21 rewritten as the single source of truth** — `Unified Roadmap`.
  Previously the Phase plan was scattered across §10.3.7 (cascade only),
  §21 (general plan), and `ARCHITECTURE_ADDENDUM_1.md` (everything else).
  Three sources diverged. v8 fixes this.
- **`ARCHITECTURE_ADDENDUM_1.md` archived** to
  `docs/archive/ARCHITECTURE_ADDENDUM_1.merged-into-v3.md` with a banner
  that points to where each topic was merged.
- §10.3.7, §17 cross-references updated to point to §21.

### P0 architectural fixes

1. **§7 (Core abstraction) — Xray as production core.** sing-box doesn't
   have a dynamic-user API. Xray has it via gRPC
   (`HandlerService.AddUser/RemoveUser` + `StatsService.QueryStats`).
   sing-box stays as **specialty core** for HY2/TUIC-inbounds and dev.
2. **§7.5 NEW — Batched Apply for cores without dynamic users.** Window
   15-30 sec, one reload per window, metrics `core_reload_total` +
   `core_reload_lost_sessions` to control cost.
3. **§10.1.2 (Wildcard) — explicit ban on `wildcard_sni + reality`.**
   REALITY relays the dest's TLS handshake; random SNI must exist at
   the dest, otherwise handshake fails. Wildcard-SNI only with operator's
   own wildcard cert over plain TLS.
4. **§14.1 (Prometheus) — per-user metrics forbidden.** Cardinality bomb
   at 100k+ users. Per-user data in Postgres (Phase 0–2) or ClickHouse
   (Phase 3+). Prometheus keeps aggregates only.
5. **§15.1 NEW — Cloudflare mTLS for agents.** CF (free) doesn't pass
   client certs to origin. Solution: separate `panel-direct` hostname
   in DNS-only / grey cloud mode. Short-lived certs (3-7 days) with
   auto-renewal; revocation = stop renewing.
6. **§19.4.4 NEW — Node Profile separation.** `reality-direct` vs
   `caddy-fronted`. Validator forbids `caddy-fronted + reality` and
   `wildcard_sni + reality`. Default `reality-direct`.
7. **§17 (MCP) — read-only default + dry-run + threat model for
   prompt-injection.** Streamable-HTTP opt-in only.

### Future / Deferred

- **§26 (Decoy Sites) → Phase 4+.** Section kept as design reference.
  Secret paths via `panel_path_config` provide baseline masking on MVP.
- **Cloudflare mTLS for agents → Phase 1.5.** Documented design only;
  implementation when we get to Agent.
- **NATS, ClickHouse, Tempo, OCI agent+core image** — explicit placement
  in Unified Roadmap, with solo-team estimates.

### Borrowed from Remnawave

- **§10.6 NEW — Subscription Profile** (Remnawave External Squads analog).
  Phase 4+. Documented as reference; orthogonal to Pool, separates
  Internal access from External view.
- **SRH Inspector** (term added to §0) — leak detection for subscription
  tokens, Phase 4+.
- **Response Rules** (term + skeleton in §10.6) — data-driven UA/IP rules
  for subscription, Phase 4+.

### DRY / consistency

- **§0 (Terms) updated:** added CoreProvider, Batched Apply, Node Profile,
  Subscription Profile, SRH Inspector, Response Rules, ADR.
- **§24 (Summary) updated** to reflect v8.
- **§23 (Open Questions) Q1 closed** — Go is decided (47 PR live).
- **Cross-references normalized** — all "Phase 2+ cascade / MCP" → "Phase 4+".
- **§10.6 ordering fixed** — was inserted before §10.5 by mistake; now
  in correct sequence.

## New files

- `docs/adr/0001-xray-as-production-core.md` — ADR for the Xray decision.
- `docs/adr/0002-node-profile-separation.md` — ADR for node profiles.
- `docs/archive/ARCHITECTURE_ADDENDUM_1.merged-into-v3.md` — archived
  addendum with merge banner.

## Modified files

- `ARCHITECTURE.md` (v7 → v8; +777 lines, -98 lines).
- `docs/guide/architecture.md` — updated pointer page with v8 changelog.

## Next steps after merge

- **#48 SubscriptionPgStore** (in plan).
- **#49 PanelCfgPgStore**.
- **#50 Xray CoreProvider** (per ADR-0001).
- **#51 Xray dynamic user add/remove** (per ADR-0001).
- **#52 BatchedApplier for sing-box** (per §7.5).
- **#53 Node Profile validator** (per ADR-0002).
- **#54 Wildcard-SNI restriction** (per §10.1.2).

## Checklist

- [x] Document consistency: single roadmap source (§21), no orphan
      cross-references to old phase numbers.
- [x] All P0 architectural errors fixed or marked as Future.
- [x] No code changes — doc-only PR.
- [x] ADRs created for major decisions (Xray, Node Profile).
- [x] Addendum archived with merge banner.
- [x] No `// TODO: fix later` left in core architectural sections.

## References

- External review: «AegisPanel architectural review» (AI-driven, 2026-07-17).
- Code state: 47 PRs merged, current branch is `main` (HEAD `c127651` →
  with #47 → `f9c8b69`).
- Related PRs (planned, not in this PR): #48, #49, #50, #51, #52, #53, #54.
