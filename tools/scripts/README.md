# Aegis — local dev tooling

A small set of Bash helpers for working with the Aegis repository. They
are intentionally POSIX-flavoured so they run on macOS, Linux, and the
WSL bash that ships with Windows.

| Script | Purpose |
| --- | --- |
| `release.sh <semver>` | Tag a release, regenerate `CHANGELOG.md`, optionally push the tag and draft a GitHub release via `gh`. |
| `restore.sh <tag> [--hard]` | Check out a previous tag. `--hard` also rewinds the current branch. Never destroys refs — always leaves a `safety/<date>` branch. |
| `backup.sh [out-dir]` | Create a `git bundle` of the entire repository (every branch, every tag, reflog) plus a manifest and sha256. Restorable with a plain `git clone <bundle>`. |
| `branch-start.sh <type> <scope/name>` | Create a Conventional-Commits feature/fix branch off the current branch. |

## Conventions

- All scripts exit non-zero on any unhandled error (`set -euo pipefail`).
- They never `rm -rf` anything outside a dedicated tempdir.
- Tags are immutable and follow SemVer (`vMAJOR.MINOR.PATCH`).
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/).
  A template is configured via `.gitmessage.txt` and `git config
  commit.template .gitmessage.txt`.
