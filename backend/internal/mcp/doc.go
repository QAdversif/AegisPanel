// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package mcp is reserved for the MCP (Model Context Protocol)
// server described in ARCHITECTURE.md §17 and the v9 roadmap
// §21 Phase 3 entry for v2.6.0 ("MCP-сервер (read-only default +
// write-scope с dry-run) — пользователи, ноды, хосты, get_stats").
//
// The MCP integration is the third-Phase item and only becomes
// useful once the admin UI is mature — exposing operator
// surfaces to an LLM agent without a fully-audited write path
// is a security non-starter.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package mcp
