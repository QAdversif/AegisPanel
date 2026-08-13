## What

The v0.8.21 live smoke test caught a seventh
silent production bug: the panel's SSH
client was happy to negotiate any of
{rsa, ecdsa, ed25519}, but the operator's
`expected_fingerprint` is for ONE specific
key. When the server picked ECDSA (not
ed25519), the fingerprint compare rejected a
perfectly-valid pin. The cdn2ne.<prod-host>.click
Demo-нода actually exposes three host keys
(rsa, ecdsa-sha2-nistp256, ed25519), and the
Go client was picking ECDSA per the kexinit
negotiation.

### Symptom (v0.8.21 live smoke)

```
bootstrap: ssh host key mismatch:
  actual   SHA256:OeZk6KcG4XcldWVtuznX3gyIjsDzNiYHFMHKsfwBDfA
  expected SHA256:<demo-node-fingerprint>
```

`ssh-keyscan cdn2ne.<prod-host>.click` exposes
exactly the three algos. The pin was the
ed25519 fingerprint; the server picked ECDSA.

## Fix

Pin the client's `HostKeyAlgorithms` to
`[ssh.KeyAlgoED25519]`. ed25519 is the
strongest deployed SSH host key algorithm,
every OpenSSH >= 6.5 supports it, and the
panel's documented support floor is
OpenSSH 7.0+. The fix is two lines (one
config field, one comment block) and makes
the fingerprint compare unambiguous: the
client refuses any host key that isn't
ed25519, so the fingerprint compare runs on
the SAME key the operator pinned.

v0.9.0 candidate: parse the algorithm from
the expected fingerprint and pin
accordingly, so an operator can pin
ed25519 OR ecdsa OR rsa. Until then, the
operator is expected to pin an ed25519
fingerprint (the convention in the v0.5.0
+ admin guide).

## Files

- `backend/internal/bootstrap/ssh.go` —
  `ssh.ClientConfig` gains a `HostKeyAlgorithms:
  []string{ssh.KeyAlgoED25519}` field. ~25
  lines of comment explaining the rationale
  and the v0.9.0 follow-up.

## Verification

The v0.8.22 live smoke test on the live server.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with `tofu_policy: accept-and-append` and
the Demo-нода's `expected_fingerprint:
SHA256:<demo-node-fingerprint>`
should return 200 with `state: "online"`.
