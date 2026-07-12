<!--
  SPDX-License-Identifier: AGPL-3.0-or-later
-->
# Pull request

## Summary

<!-- One or two sentences. What does this PR change and why? -->

## Linked issues

<!-- Use `Closes #123` to auto-close issues, or `Refs #123` for related work. -->

## Type of change

- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Breaking change (fix or feature that would change existing behaviour)
- [ ] Documentation only (no code change)
- [ ] Refactor / cleanup (no behaviour change)

## How was this tested?

<!-- Checklist:
     - [ ] `make lint` passes
     - [ ] `make test` passes
     - [ ] Manual scenario:
     - [ ] New unit / integration tests:
-->

## Checklist

- [ ] My code follows the project's [CONTRIBUTING.md](../../CONTRIBUTING.md)
- [ ] I have added tests that prove the fix / feature works
- [ ] New and existing unit tests pass locally (`make test`)
- [ ] I have updated the relevant docs (`/docs/`, `ARCHITECTURE.md`)
- [ ] I have added a `CHANGELOG.md` entry under "Unreleased" (if user-facing)
- [ ] I have updated the conventional-commit type in my commit subject
      (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`, …)
- [ ] I have run `tools/scripts/backup.sh` before and after the change
      when touching anything that mutates state

## Security & privacy

- [ ] No new secrets are committed (`.env`, keys, tokens, certs)
- [ ] I have considered the AGPL-3.0 disclosure requirement: any
      changes that ship in a hosted/network-accessible deployment must
      be open-sourced
- [ ] I have not added any external services that send user data
      outside the operator's infrastructure without their consent
