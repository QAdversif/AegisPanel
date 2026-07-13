# Security policy

## Supported versions

Aegis is in **pre-alpha** (Phase 0 scaffolding). Security fixes are
backported to the latest minor only.

| Version | Supported |
| --- | --- |
| `main` branch (development) | ✅ |
| Latest tagged release (`v*.*.*`) | ✅ |
| Older releases | ❌ |

## Reporting a vulnerability

**Please do not file a public issue.** Use one of these private
channels instead:

1. **GitHub Security Advisories:** open a
   [private security advisory](https://github.com/QAdversif/AegisPanel/security/advisories/new)
   on the repository. This is the preferred channel — it lets us
   coordinate a fix and a coordinated disclosure timeline.
2. **Email:** `security@QAdversif.example` (replace with the
   real address once the public repo is set up). Encrypted reports
   are welcome; the PGP fingerprint will be published alongside the
   address.

Include:

- A clear description of the issue and its impact.
- A minimal, self-contained reproduction (PoC, screenshot, or curl
  trace).
- The affected commit / tag / container image.
- Your name / handle if you'd like to be credited in the advisory.

## What to expect

- **Acknowledgement:** within 72 hours.
- **Initial assessment:** within 7 days.
- **Status updates:** at least every 14 days until disclosure.
- **Coordinated disclosure:** we aim to publish a fix *and* an
  advisory at the same time. We will agree the disclosure date with
  you; the default is 90 days from acknowledgement, negotiable.

## Scope

In-scope issues include:

- Authentication, authorisation, and session handling in the panel.
- Secrets handling, JWT signing, password storage.
- Sandbox escapes in the decoy-site upload / render path.
- RCE / privilege escalation via the BYO Node bootstrap path.
- Subscription-token leakage, IDOR on user data.
- Supply-chain issues in pinned Go / npm / Docker dependencies.

Out of scope (please report to upstream):

- Vulnerabilities in `sing-box`, `Xray`, `Hysteria 2`, Caddy, fail2ban,
  Postgres, Redis, NATS, or ClickHouse.
- Generic web vulnerabilities in default decoy presets (they are
  examples, not part of the security boundary).

## Recognition

We follow a
[hall of fame](https://github.com/QAdversif/AegisPanel/security/hall-of-fame)
model. Reporters who follow responsible disclosure are credited (with
their consent) in the corresponding GitHub Security Advisory.

## Why this matters

Aegis is AGPL-3.0. Security issues affect every operator. Reporting
privately gives the maintainers time to ship a fix before the
details become a public playbook for attackers. We are committed to
treating reporters with respect, fixing issues quickly, and
crediting the work.
