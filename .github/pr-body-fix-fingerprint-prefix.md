## What

The v0.8.22 live smoke test caught an eighth
silent production bug: the fingerprint
compare returned `false` even when the
fingerprints matched, because one side
included the `SHA256:` prefix and the other
did not. v0.8.22 forced the ed25519 key path
so both sides now compute the same fingerprint,
but `fingerprintEqual` did a literal string
compare and rejected `pCnGi…` ≠ `SHA256:pCnGi…`.

### Symptom (v0.8.22 live smoke)

```
bootstrap: ssh host key mismatch:
  actual   pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM
  expected SHA256:pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM
```

The fingerprints are the SAME. The format
is the only difference.

## Fix

`fingerprintEqual` now strips a leading
`SHA256:` or `MD5:` algorithm prefix
(case-insensitive) from both sides before
comparing. The function takes either form —
the panel's internal `sshFingerprintWire`
returns just the base64 payload, the
operator's paste from `ssh-keygen -lf` has
the `SHA256:` prefix.

A new `stripFingerprintPrefix` helper does
the prefix detection (case-insensitive via
`strings.ToUpper` + `strings.HasPrefix`)
and the slice. Unknown prefixes are passed
through unchanged, so a future algorithm
change (e.g. `SHA512:`) surfaces as a real
mismatch.

`TestFingerprintEqual` is now a table-driven
test with five cases (case-insensitive,
different base64, mixed prefix, MD5 prefix,
unknown prefix).

## Files

- `backend/internal/bootstrap/ssh.go` —
  new `stripFingerprintPrefix` helper;
  `fingerprintEqual` updated to call it.
- `backend/internal/bootstrap/ssh_test.go` —
  `TestFingerprintEqual` rewritten as
  table-driven; 5 cases.

## Verification

v0.8.23's live smoke test on the live server.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with `tofu_policy: accept-and-append` and
`expected_fingerprint: SHA256:pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM`
should return 200 with `state: "online"`.
