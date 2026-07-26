# chore(repo): drop tracked `.git-commit-*.txt` file + add throwaway patterns to `.gitignore`

## TL;DR

PR #97 (d-refactor.2) accidentally tracked the commit-message draft
file `.git-commit-d-refactor.2.txt` at the repo root. The throwaway-
file convention says these files are scratch space and must be
mavis-trash'd before `git add`; the .gitignore did not have a
matching pattern to enforce this at the indexer level. This PR
removes the tracked file and adds the throwaway patterns to
`.gitignore` so the next PR cycle cannot repeat the footgun.

## Why a follow-up PR (not amend)

The d-refactor.2 commit is already on `main` and the branch was
squash-merged + deleted. An amend would require either rewriting
the merge commit (forbidden on protected branches) or a force-push
(also forbidden). A 2-file chore PR is the cheapest fix that does
not bend the policy.

## Changes

- `rm .git-commit-d-refactor.2.txt` (the file that was
  accidentally tracked in #97).
- `.gitignore`: add `.git-commit-*.txt` + `.tmp-*.{py,log,json,sh}`
  to the throwaway-local-files block. The `.github/pr-body-*.md`
  pattern is intentionally NOT in this list — those files are
  tracked on purpose (PR body convention).
- Doc comment on the new block explains the convention and
  points to agent memory.

## Verification

- `git ls-files | grep .git-commit` returns empty.
- `git status` after creating a throwaway file: ignored.
