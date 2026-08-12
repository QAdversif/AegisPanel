## What

The v0.8.19 live smoke test caught a fifth
silent production bug: a fresh install of
the panel can't provision its first node
because the SSH handshake fails with
"knownhosts: key is unknown" — even when
the operator passed the correct
`expected_fingerprint` and `tofu_policy:
"accept-and-append"`.

### Root cause

`backend/internal/bootstrap/ssh.go:362-368`
(pre-PR) had an early-return on a successful
`knownhosts.New(knownHostsPath)` call. The
function returned the strict `knownhosts.New`
callback (which rejects anything not in the
file) **before** the TOFU policy switch was
ever consulted. The comment above the
return claimed "the TOFU layer replaces it
below" — the function then fell through to
a dead branch that the return statement
prevented from running.

On a fresh install the panel's
`/var/lib/aegis/known_hosts` is a 0-byte
file (mounted by docker-compose). The
strict `knownhosts.New` callback loads the
empty file, then rejects every key the SSH
server presents. The TOFU fingerprint
compare — which the operator's
`expected_fingerprint` was supposed to
satisfy — never runs. Result: the
`POST /api/v1/nodes/{id}/provision` request
returns 502 with `error: "bootstrap: install
failed at stage \"connect\": ... knownhosts:
key is unknown"`, and the panel never
appends the first key.

## Fix

The TOFU policy IS the callback. The
known_hosts lookup is invoked *inside* the
`TofuAcceptAndAppend` branch (and inside
`TofuReject`), never as an early exit. The
control flow is now:

  1. If the known_hosts file is absent, every
     key falls through to the TOFU policy
     callback.
  2. If the known_hosts file exists, the
     callback tries the inner
     `knownhosts.New` lookup first. A match
     accepts the key silently. A miss falls
     through to the TOFU policy callback
     which compares against
     `ExpectedFingerprint` and (on match)
     stashes the key for the post-handshake
     append.

The pre-PR early-return was deleted. The
fallback `TofuReject` branch still returns
`ErrHostKeyUnknown` for an unknown key with
the reject policy. The `TofuAcceptAndAppend`
branch still does the fingerprint compare
and key stash. The behavior the existing
tests expect is preserved; the only
behavior change is that the TOFU policy
is now reachable on a fresh install.

## Tests

Three regression tests in
`backend/internal/bootstrap/ssh_test.go`:

  - `TestHostKeyCallback_EmptyKnownHosts_TOFU_Accepts`:
    the v0.8.19 bug. Empty `known_hosts` +
    `TofuAcceptAndAppend` + matching
    `ExpectedFingerprint` → callback returns
    nil AND `c.tofuKey` is stashed for the
    post-handshake append. Pre-PR this
    returned `knownhosts: key is unknown`.
  - `TestHostKeyCallback_KnownKey_Accepted`:
    an existing `known_hosts` entry is
    accepted silently and the
    `ExpectedFingerprint` (mismatched in the
    test) is ignored. `c.tofuKey` is
    **not** stashed — the key is already in
    the file, no re-append.
  - `TestHostKeyCallback_EmptyKnownHosts_RejectsOnMismatch`:
    safety net. Empty file + `TofuAcceptAndAppend`
    + a fingerprint that does NOT match the
    presented key → `ErrHostKeyMismatch`.

## Files

- `backend/internal/bootstrap/ssh.go` —
  the `hostKeyCallback` function. ~70 lines
  of code change inside the function body,
  no signature change.
- `backend/internal/bootstrap/ssh_test.go` —
  three new tests + `net` import.

## Verification

The v0.8.20 live smoke test on aibeg.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with the Demo-нода's `tofu_policy: accept-and-append`
and `expected_fingerprint: SHA256:pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM`
should return 200 with `state: "online"`
(transition from `new` → `provisioning` → `online`).
