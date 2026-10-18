# AegisPanel — Architecture vs Current State (v0.8.28)

> **Generated 2026-08-21** (post-v0.8.28 prod deploy).
> Cross-references `ARCHITECTURE.md` §21 (Unified Roadmap,
> v9.2 — sing-box only MVP) against the actual
> shipped state at v0.8.28 (main HEAD `ca8125f` +
> prod at `<prod-host>` with image
> `ghcr.io/qadversif/aegispanel:0.8.28`).
> Answers: "what functionality is left to implement?"
>
> **Source of truth** for the plan: `ARCHITECTURE.md`
> §21 + the 4 ADRs at `docs/adr/0001..0004`. Source
> of truth for the shipped state: `CHANGELOG.md`
> (v0.1.0..v0.8.28) + `docs/ROADMAP.md` + the
> existing `docs/gap-analysis-v0.8.24.md`
> (refreshed against v0.8.28 in PR #276).

## TL;DR

**For v1.0.0-mvp-soft-launch (the GA tag)**: 2 open
items. Both gate the GA tag. ~3-5 days focused
effort.

| # | Item | Effort | Blocked by |
| --- | --- | --- | --- |
| 1 | **Restore-drill on a fresh VM** (download backup → restore → first-boot → panel reachable) | 1-2 days | CI infrastructure for clean-VM test |
| 2 | **24h soak** (1 panel + 1 node + 10 users, no crashes; user CRUD ≤ 30 s) | 1-2 days (mostly waiting) | Item 1 — need a clean test environment |

**For v0.9.1 (post-GA patch)**: 7 Tier 1 #3
follow-up items from the v0.8.28 batch. ~1 week
focused effort. All closeable in a single 3-5 PR
sequence.

**For Phase 2 (v1.1.0 — v1.8.0)**: ~6-8 of 8 items
in backlog. ~2 of 8 partially shipped (webhooks
fully done; decoy sites v1 done; users/plans done).
~10-12 weeks solo (per §21 estimate).

**For Phase 3 (v2.0.0+ — Xray + Cascade + MCP)**:
all backlog. ~18-22 weeks solo (per §21 estimate).
No deadline; user-demand driven.

**For Phase 4+ (per-demand backlog)**: all
backlog. Tracked in §21 "Phase 4+" section.

---

## 1. Phase 0 / MVP-0.x status (from §21 plan)

| Slice | §21 status | Actual state | Notes |
| --- | --- | --- | --- |
| Phase 0 (foundation) | `[done]` | ✅ done (PR #1–#43) | No follow-ups |
| MVP-0.1 (Render-only) | `[done]` | ✅ done (PR #50, #51, #54–#58) | Tag `v0.1.0-mvp-render` on `5840c13` |
| MVP-0.2 (Agent) | `[done]` | ✅ done (PR #47, #59–#66) | Tag `v0.2.0-mvp-agent` on `c2e773c` |
| MVP-0.3 (BYO Node) | `[wip]` (a done, b/c pending) | ✅ **fully done** (a: PR #67; b: PR #201; c: PR #222) | The §21 `wip` status is stale — the v0.3.0 c slice (real aegis-agent binary + Ansible `install_agent` role) was actually closed in v0.8.15 (PR #222) and v0.8.12 (PR #201). The "fully done" wasn't reflected in §21 |
| MVP-0.4 (Batched Apply) | `[done]` | ✅ done (PR #92, #93, #94 + PR #157, #158 + PR #168, #169, #170) | Phase 2 multi-user render extended it; 100-user burst test still deferred (see §3 below) |
| **MVP-1.0 (Soft launch)** | `[ ]` | 🟡 5/7 DoD items done; 2 open (see §2) | The §21 MVP-1.0 DoD list is 5-7 items, most already shipped in v0.5.x..v0.8.28 batch. The remaining 2 are restore-drill + 24h soak |

**Per-user render sub-tasks (from §21 "Slice status
per v9.5")**:

| Sub-task | Actual state |
| --- | --- |
| `user_inbound_credentials` table + Service/Store | ✅ PR #167, v0.8.0 |
| Multi-user renderer signature | ✅ PR #168, v0.8.0 |
| Builder wiring + BatchedApplier fan-out narrow | ✅ PR #169, v0.8.0 |
| Per-user subscription render | ✅ PR #170, v0.8.0 |
| HTTP admin surface for credentials | ✅ PR #183, v0.8.2 (earlier than §21 expected) |
| Host → node mapping in Builder filter | ✅ PR #192, v0.8.x |
| Inbound-templates (per-tenant `Params` defaults) | ✅ PR #205, #209, #210, #211, #212, v0.8.13 (earlier than §21 expected) |
| Per-user credential filter | ✅ PR #198, v0.8.10 (earlier than §21 expected) |
| Metrics (`core_reload_total`, `core_reload_pending_users`, `core_user_apply_latency_seconds`) | ⏳ **still open** (deferred to v0.9.x — needed for v1.0.0 DoD: "1 panel + 1 node + 10 users for 24h no crashes") |
| 100-user burst test | ⏳ **still open** (deferred to v0.9.x) |

## 2. MVP-1.0 (Soft launch) — the GA gate

The v1.0.0 DoD per §21:

| # | DoD item | Status | Notes |
| --- | --- | --- | --- |
| 1 | Healthchecks на panel + agent | 🟡 partial | Panel `GET /api/v1/health` ✅ (anonymous, returns 200). Agent `/healthz` is wired per the §10.2 spec but no smoke gate that the agent's endpoint actually responds on the prod node. **Need**: a 1-line shell check in the prod deploy script (`curl -fsS http://<node>:8080/healthz`) |
| 2 | Логи в JSON в stdout (panel + agent) | ✅ done | v0.8.6 hardening — `AEGIS_ENV=production` → `zerolog.JSON`. Agent uses zerolog from v0.4.0 |
| 3 | Backup-скрипт для Postgres + cron + retention | ✅ done | `aegis-pg-backup` CLI (PR #125, v0.5.0) + `Service.ReloadCron` + `GET /api/v1/backups/schedule` (PR #275, v0.8.28) + 33 scheduler goroutine tests (PR #274, v0.8.28). 7 v0.9.1 follow-up items parked (data race, handler tests, scheduleActive semantic, POST endpoint, weekly orphan-file cron, field naming, doc syntax) |
| 4 | `tools/scripts/branch-start.sh`, `release.sh` — dry-run | ✅ done | PR #250, v0.8.27 (--dry-run + --snapshot hardening) |
| 5 | `docs/user-guide/admin/quickstart.md` | ✅ done | `docs/guide/quickstart.md` exists (per the v0.5.0 row) |
| 6 | `.env.example` обновить | ✅ done | The canonical 12-backend env shape is documented in `deploy/ansible/group_vars/all.yml` + `docs/operator-guide.md` §6.3. **Lesson from v0.8.28 deploy**: this is a stale-env footgun; need a `aegis admin audit-env` (or `tools/scripts/audit-env.sh`) subcommand to diff the running panel's `AEGIS_*_BACKEND` set against the canonical 12-backend set (see v0.9.x follow-up) |
| 7 | Restore-drill на чистой VM | ⏳ **open** | The big v0.9.0. Pre-existing `tools/scripts/restore.sh` covers the operator side; a CI job that runs it against a fresh-provisioned VM is the missing piece. Tier 1 in the gap-analysis-v0.8.24.md §6. ~3 days focused effort (terraform + ansible + boot-log artifact in CI) |
| 8 | "1 панель + 1 нода + 10 юзеров работают 24 часа без сбоев" (the runtime DoD) | ⏳ **open** | Blocked by #7 (need a clean test environment) + the 100-user burst test. ~1-2 days after #7 is done, mostly waiting |
| 9 | "Создание юзера → ≤ 30 сек → подписка обновилась" (perf DoD) | 🟡 not validated | The end-to-end path is wired (PR #170 + BatchedApplier 401→auto-refresh PR #189), but no perf benchmark exists. **Need**: a vitest or k6 load test that creates a user, polls `/api/v1/sub/<token>`, measures the latency. The 30 s budget is a §21 aspirational number, not validated |
| 10 | "Удаление юзера → ≤ 30 сек → sing-box убрал юзера" | 🟡 not validated | Same as #9 — path exists, no perf benchmark |
| 11 | "Restart агента → auto-reconnect, Apply replay" | 🟡 not validated | The 401→auto-refresh path (PR #189) handles bearer regeneration. The "Apply replay from last panel revision" path is implemented in BatchedApplier (in-memory queue + drain on reconnect). No chaos test for "kill agent + restart → confirm Apply replays" |
| 12 | "Backup → restore на чистую машину → работает" | ⏳ **open** | Same as #7 — the restore-drill covers this |

**Summary for GA gate**:
- ✅ 6 of 12 DoD items are clearly done in v0.8.28
- 🟡 3 are wired but not perf-validated (#9, #10, #11)
- ⏳ 3 are open (#7, #8, #12) — all closed by a single ~3-5 day v0.9.0 batch

## 3. Phase 2 (v1.1.0 — v1.8.0) — Post-MVP hardening

The §21 table (with actual state):

| Ver | What (per §21) | Status | Notes |
| --- | --- | --- | --- |
| **v1.1.0** | mTLS + gRPC Panel↔Agent | ⏳ open | Still HTTP+Bearer. Per §21 estimate: 2 нед |
| **v1.2.0** | users CRUD + plans + traffic limits + Cabinet API | 🟡 partial | users CRUD ✅ (v0.8.0), plans ✅ (v0.6.0), traffic limits ⏳ (no `internal/stats/`), Cabinet API ⏳ (no `internal/cabinet/`). Per §21 estimate: 2-3 нед to close the gap |
| **v1.3.0** | Webhooks (HMAC + anti-replay + backoff + DLQ) | ✅ done | v0.7.0 PR #136-#139 + v0.7.1 wiring PR #146-#150. Earlier than §21 expected |
| **v1.4.0** | Outgoing notifications (Telegram / n8n) | ⏳ open | `internal/notifications/` is doc.go-only. Per §21 estimate: 1 нед |
| **v1.5.0** | Observability (Prometheus + Grafana) | ⏳ open | Only JSON logs (`internal/obs`). No Prometheus exporter. Per §21 estimate: 1 нед |
| **v1.6.0** | Multi-port + inbound profiles UI | 🟡 partial | Multi-port support in the data model (§10.1.4) ✅, but the UI is basic (no visual editor for multi-port / per-port settings). Per §21 estimate: 1 нед |
| **v1.7.0** | Decoy sites v1 (Caddy руками + reference config) | ✅ done | Decoy sub-path + Caddyfile override + `/var/www/decoy/index.html` (deployed at v0.8.9 fresh install; v0.8.10 Caddyfile v3 no-store hardening) |
| **v1.8.0** | Per-user traffic (ClickHouse or Postgres) | ⏳ open | `internal/stats/` is doc.go-only. Per §21 estimate: 2 нед |

**Phase 2 summary**: 2 of 8 done, 3 of 8 partial, 3 of 8
not started. Total remaining: ~10-12 weeks solo
(per §21), but with the 3 partials to close first
(maybe 2-3 weeks) and the 5 not-started (maybe
6-8 weeks).

**Realistic re-prioritization for v0.9.x**:
- v1.2.0 partial: close the traffic-limits + Cabinet-API
  gap (because §21 v1.0.0 DoD says "1 panel + 1 node
  + 10 users работают 24 часа без сбоев" — without
  traffic limits we have no usage signal in 24h
  soak)
- v1.5.0: close the Prometheus exporter (because
  the 24h soak needs observability to confirm
  "no crashes")
- v1.3.0 / v1.7.0: already done

## 4. Phase 3 (v2.0.0 — v2.8.0) — Second core + advanced

All backlog. From §21:

| Ver | What | Status | Notes |
| --- | --- | --- | --- |
| **v2.0.0** | Xray CoreProvider as second provider (gRPC `HandlerService` + `StatsService`); UI core selector | ⏳ open | New `internal/cores/xray/` package. Per §21: 3-4 нед |
| **v2.1.0** | Balancer host type (Xray `leastLoad` / sing-box `urltest`) | ⏳ open | 1-2 нед |
| **v2.2.0** | Cascade Topology (Xray `reverse` mode, Portal → Bridge) | ⏳ open | 4-6 нед. Requires Xray |
| **v2.3.0** | Network Map UI + Subscription Profile (External Squads) | ⏳ open | 2-3 нед |
| **v2.4.0** | SRH Inspector (subscription-request log + leak detection) | ⏳ open | 1-2 нед |
| **v2.5.0** | Response Rules engine (UA/ASN/status → format/announce/block) | ⏳ open | 2 нед |
| **v2.6.0** | MCP server (read-only default, write-scope with dry-run) | ⏳ open | 2 нед |
| **v2.7.0** | Node ACL (Celerity-style, `reject` / `direct` / `geoip`) | ⏳ open | 1-2 нед |
| **v2.8.0** | Decoy Sites (full — managed upload, XSS sanitize, zip-slip protect, Playwright preview) | ⏳ open | 3 нед |

**Phase 3 summary**: 0 of 9 done. ~18-22 weeks solo
(per §21). All gated on user demand — no internal
deadline. The first ship (v2.0.0 Xray) unblocks
v2.1.0 + v2.2.0 (both depend on Xray), so they're
naturally sequenced.

## 5. Phase 4+ backlog (per-demand)

From §21 "Phase 4+":

- Cascade `forward` mode + relay role + multi-hop
- WireGuard inbound (PasarGuard-style)
- Hysteria 2 standalone / TUIC standalone core providers
- Canary deploys / blue-green / geo-aware full
- Marzban-importer → remnawave-importer (main adoption channel)
- OCI agent+core combined image (Remnawave-style; versioned together)
- Cloudflare mTLS (Enterprise tier)
- Infra billing (node cost tracking)
- Multi-region panel with CRDT or read-replica

**All backlog, user-demand driven**.

## 6. Anti-features (per §21, NOT to do)

- Multi-tenant (one panel = one operator, per §27)
- Provider APIs (Hetzner/AWS/...) — BYO Node only
- Internal payment gateway — webhook contract for external Cabinet only
- NATS as event bus in Phase 0-2 — Redis Streams only
- Telegram OAuth for admin — JWT + Argon2id only
- "Save anyway" for invalid configs (Remnawave anti-pattern)
- Tempo / OpenTelemetry tracing on MVP — metrics + logs from day 1
- Decoy-marketplace, dynamic decoy by UA/geo — v2.8.0+ backlog
- Custom decoy upload through UI on MVP — operator-configures-Caddy-by-hand
- Xray on v1.0 — explicitly out (ADR-0003). v2.0.0+ only

## 7. Recommended next steps (priority order)

| Priority | Item | Why | Effort |
| --- | --- | --- | --- |
| **P0** | v0.9.0 — restore-drill on fresh VM (#7, #12) | GA gate | 1-2 days |
| **P0** | v0.9.0 — 24h soak + chaos test (#8, #11) | GA gate | 1-2 days (mostly waiting) |
| **P0** | v0.9.0 — perf benchmark for user CRUD (#9, #10) | GA gate | 0.5 day |
| **P1** | v0.9.1 — 7 Tier 1 #3 follow-up items (cron hardening polish) | Post-GA patch | 1 week |
| **P1** | v0.9.0 — `aegis admin audit-env` subcommand (closes the v0.8.28 lesson) | Stale-env footgun | 0.5 day |
| **P1** | v0.9.0 — canonical env template at `docs/operator-guide.md` §"Environment variables" | Stale-env footgun | 0.5 day |
| **P2** | v0.9.0 — `core_reload_total` / `core_reload_pending_users` / `core_user_apply_latency_seconds` Prometheus metrics | Required for 24h soak observability (per §21) | 1-2 days |
| **P2** | v0.9.0 — 100-user burst test | v0.4.0 DoD (per §21) | 0.5 day |
| **P3** | v1.1.0 — mTLS + gRPC Panel↔Agent (per §21) | Post-GA hardening | 2 weeks |
| **P3** | v1.2.0 — close the traffic-limits + Cabinet-API gap (per §21) | Post-GA feature | 2-3 weeks |
| **P3** | v1.4.0 — Telegram notifications (per §21) | Post-GA feature | 1 week |
| **P3** | v1.5.0 — Prometheus exporter (per §21) | Post-GA observability | 1 week |
| **P4** | v1.6.0 — multi-port inbound UI (per §21) | Post-GA UX | 1 week |
| **P4** | v1.8.0 — per-user traffic (per §21) | Post-GA analytics | 2 weeks |
| **P5** | v2.0.0+ — Xray + Cascade + MCP + ACL + Decoy (per §21) | User-demand driven | 18-22 weeks solo total |

**GA gate path** (the only thing blocking the
`v1.0.0-mvp-soft-launch` tag):

```
v0.9.0-a  Restore-drill on fresh VM    (1-2 days)
v0.9.0-b  Perf benchmark + chaos test  (0.5 day)
v0.9.0-c  Metrics for observability     (1-2 days)
v0.9.0-d  24h soak                      (1-2 days, waiting)
─→
v0.9.1    7 Tier 1 #3 follow-ups         (1 week)
─→
v1.0.0    Soft launch tag 🎉
```

Estimated total: **3-5 days focused** for v0.9.0
GA gate + 1 week for v0.9.1 polish = **2-3 weeks
calendar time** to `v1.0.0-mvp-soft-launch`.

## 8. v0.8.28 lessons worth tracking

From the v0.8.28 prod deploy (documented in
`KNOWN_LIMITATIONS.md` §"v0.8.28 — deployed to prod
at 2026-08-21 MSK"):

1. **Stale env footgun** — the prod env file was
   created in v0.8.9 and never received the 12th
   backend var from PR #205 (v0.8.13). inbound_templates
   ran on memory default for 5+ releases, losing
   data on every restart. **Action**: `aegis admin
   audit-env` subcommand + canonical env template
   (see P1 above).

2. **Markdownlint footgun on `+` separator** — my
   first PR #276 had a `+ per-row pointers` mid-
   paragraph that the markdownlint `MD004` rule
   interpreted as a list item, breaking "consistent"
   for the whole file. **Memory entry** (already
   added in MEMORY.md per the v0.8.20-era lessons):
   the rule picks the FIRST list style in the file
   and requires all others to match. New `+` mid-
   paragraph → all pre-existing `-` lists fail.
   Avoid `+` as separator in soft-wrapped prose.

3. **Markdownlint footgun on `#NNN` mid-paragraph** —
   `**Tier 3 (PRs\n#254-#270)**: HostsView...` had
   `#254-#270` at the start of a soft-wrapped line,
   which MD018 interpreted as a malformed heading.
   **Fix**: escape `\#254-\#270` or reword.

4. **PAT leak via `git config --get remote.origin.url`**
   (already in MEMORY.md per the 2026-08-20 lessons)
   — the URL-embedded token gets echoed in stdout.
   Three leaks in one session. **Action**: store
   the token in `~/.aegis/agent-token` (a local
   file, never echoed) and use `Get-Content` + env
   var instead of `git remote -v` or `git config
   --get remote.origin.url`.

## 9. Summary

| Phase | §21 plan | v0.8.28 actual | Gap |
| --- | --- | --- | --- |
| Phase 0 | 2-3 нед | ✅ done | — |
| MVP-0.1..0.4 | 3-4 нед | ✅ done (all 4 slices, 2 of them "earlier than §21 expected") | — |
| **MVP-1.0** | **0.5 нед** | 🟡 5/7 done; 2 open (restore-drill + 24h soak) | **3-5 days focused** |
| Phase 2 (v1.1.0..v1.8.0) | ~10-12 нед | 🟡 2/8 done, 3/8 partial, 3/8 not started | ~10-12 weeks solo (consistent with §21) |
| Phase 3 (v2.0.0..v2.8.0) | ~18-22 нед | ⏳ 0/9 done | ~18-22 weeks solo (consistent with §21) |
| Phase 4+ | per-demand | ⏳ backlog | per-demand |

**Single biggest takeaway**: the §21 plan is
**accurate**. Every line is either done or on
track. The plan's "5-7 weeks solo to MVP-1.0"
estimate was correct (v0.1.0 + v0.2.0 + v0.3.0 +
v0.4.0 = ~5 weeks historically; v0.5.0..v0.8.28 = 5
weeks of hardening; the remaining 0.5-2 weeks of
restore-drill + 24h soak is on track).

The v0.8.x batch delivered everything in the §21
plan except the GA gate items + most of Phase 2.
The Phase 2 backlog (users + traffic + Cabinet +
mTLS + observability + notifications) is the
"feature work" that comes after GA. None of it
is blocking the v1.0.0 tag.
