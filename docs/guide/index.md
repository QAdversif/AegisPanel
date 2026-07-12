---
title: What is Aegis?
---

# What is Aegis?

**Aegis** is a self-hosted control panel for running a multi-protocol VPN
service. It manages a fleet of *nodes* (VPS instances that run the actual
proxy daemon) and provides the operator with a unified UI, a REST API, a
subscription-rendering service for end-user clients, and a number of
anti-censorship conveniences (Caddy reverse proxy, decoy HTML sites,
port masquerading, SSH-based brute-force protection).

## Who is it for?

- A single operator who wants to run a commercial or community VPN
  service without depending on a hosted panel.
- A small team that needs scripted, API-driven provisioning.
- A privacy-focused user that wants full control over their stack.

Aegis is **single-tenant**: one panel installation serves one operator.
Multiple admin accounts with role-based access control are supported
inside a single tenant.

## Core ideas

- **Multi-core.** The panel talks to the proxy daemon through a
  `CoreProvider` abstraction. The MVP uses [sing-box](https://sing-box.sites/);
  Xray, Hysteria 2, and others can be plugged in without UI rewrites.
- **BYO Node.** The operator provides a VPS (or many) and SSH credentials;
  the panel installs and operates the daemon via Ansible. There is no
  cloud-provider API integration — the operator owns the lifecycle of
  every server.
- **Subscription compatibility.** End users can import the subscription
  URL into any popular client: Hiddify, v2rayNG/N, Streisand, NekoBox,
  Karing, V2Box, Clash Verge / Meta, and others. The panel auto-detects
  the client via `User-Agent` and serves the right format
  (sing-box JSON, Clash Meta YAML, or base64 URI list).
- **Anti-censorship.** Caddy terminates TLS on popular web ports
  (`443`, `2053`, `2083`, `2087`, `2095`, `2096`, `8443`). The default
  domain serves a `decoy` HTML site; the actual panel / proxy lives
  behind a randomly generated secret path. Sing-box / Xray falls back
  invalid handshakes to the decoy.
- **Open source.** AGPL-3.0. Anyone offering Aegis as a hosted service
  must publish their modifications.

## What it is *not*

- **Not multi-tenant.** One panel = one operator. If you want to host
  multiple operators, deploy multiple panels.
- **Not a full automation suite for cloud providers.** No Hetzner / AWS
  / DigitalOcean / Linode API integration. The operator brings their
  own servers.
- **Not a billing platform.** The cabinet API is a contract for an
  external personal-account / payment service. The panel itself
  exposes the user lifecycle; payment processing is the cabinet's job.
- **Not a UI framework showcase.** The admin UI is a Vue 3 + Vite SPA
  tuned for ops work, not a designer's playground.

## Where to next?

- [Architecture](./architecture) — the full design document.
- [Getting started](./getting-started) — bringing up the dev stack.
