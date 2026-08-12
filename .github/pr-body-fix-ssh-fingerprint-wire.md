## What

The v0.8.20 live smoke test caught a sixth
silent production bug: the `expected_fingerprint`
compare in the TOFU path used the wrong hash
function. The operator-supplied fingerprint
(identical to `ssh-keygen -lf` output)
**never matched** the panel's computed hash,
so the very first provision on a fresh install
returned `ErrHostKeyMismatch` even when the
operator's pin was correct.

### Root cause

`backend/internal/bootstrap/ssh.go` (pre-PR)
called `ssh.FingerprintSHA256(key)` to compute
the host-key fingerprint. This is a long-standing
misnomer in `golang.org/x/crypto/ssh`: the
function hashes the **authorized_keys LINE**
format (`"ssh-ed25519 AAAA...\n"`), not the
**binary wire format** (`base64-decode("AAAA...")`).
The two hashes are different, and the
OpenSSH-standard format is the binary wire one
(what every operator pastes from `ssh-keygen -lf`).

The v0.8.20 fix in PR #230 made the TOFU path
reachable for the first time. It immediately
surfaced this latent bug: the TOFU callback
ran, the fingerprint compare ran, and the
compare failed with a spurious mismatch.

## Fix

Replace `ssh.FingerprintSHA256(key)` with a
custom `sshFingerprintWire(key)` that computes
the SHA-256 of `key.Marshal()` — the binary
wire format — and returns the result as
base64 with the trailing `=` padding stripped
(to match `ssh-keygen -lf` byte-for-byte).

The fix is one function + one call site:

```go
// sshFingerprintWire computes the SHA-256
// fingerprint of the BINARY WIRE format ...
// (this matches what ssh-keygen -lf emits)
func sshFingerprintWire(key ssh.PublicKey) string {
    h := sha256.New()
    h.Write(key.Marshal())
    return strings.TrimRight(
        base64.StdEncoding.EncodeToString(h.Sum(nil)),
        "=")
}

// in the TOFU callback:
actual := sshFingerprintWire(key)  // was: ssh.FingerprintSHA256(key)
```

## Tests

`TestSshFingerprintWire_MatchesOpenSSH`
locks in the fix with a real-world fixture:
the production Demo-нода's host key, captured
via `ssh-keyscan -t ed25519`. The test asserts:

  1. `sshFingerprintWire(pubKey)` returns
     `pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM`
     — the exact value `ssh-keygen -lf` would
     print and the exact value the operator
     confirmed at deploy time.
  2. `ssh.FingerprintSHA256(pubKey)` returns a
     DIFFERENT value (proving the legacy Go
     function is the bug; if a future Go
     library version fixes this, the test
     fails loudly so the panel can drop the
     custom helper).

The pre-existing TOFU regression tests in
`TestHostKeyCallback_*` were updated to use
`sshFingerprintWire` (the new helper) instead
of `ssh.FingerprintSHA256` (the broken legacy
helper) for their `ExpectedFingerprint` setup.

## Files

- `backend/internal/bootstrap/ssh.go` —
  new `sshFingerprintWire` function + import
  for `crypto/sha256` and `encoding/base64`;
  one call site changed.
- `backend/internal/bootstrap/ssh_test.go` —
  new `TestSshFingerprintWire_MatchesOpenSSH`
  with the real Demo-нода key fixture; the
  three pre-existing TOFU tests use the new
  helper.

## Verification

The v0.8.21 live smoke test on the live server.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with `tofu_policy: accept-and-append` and
`expected_fingerprint: SHA256:pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM`
should return 200 with `state: "online"`.
