#!/bin/bash
# ==============================================================================
# Bazel workspace status - the build's identity keys
# ==============================================================================
# Run by Bazel before every build via `--workspace_status_command`. Its stdout is
# parsed as a set of key/value pairs, so the format is a contract, not a
# convention. The rules, each enforced by :workspace_status_contract_test beside
# this file:
#
#   1. One "KEY value" pair per line. The key is upper-case letters and
#      underscores only. The FIRST space separates the key from the value; the
#      value is the rest of that line.
#   2. Neither the key nor the value may span multiple lines. Bazel splits this
#      output on newlines, so a value containing one is truncated to its first
#      line and the remainder is parsed as a second, bogus key. That is a real
#      failure mode, not a theoretical one: `git rev-parse HEAD` in a repository
#      with no commits prints "HEAD" on stdout AND exits 128, so the common
#      `$(git rev-parse HEAD || echo unknown)` captures two lines and stamps
#      `HEAD` as the commit.
#   3. Keys must be unique.
#   4. Exit 0, or the build fails before it starts.
#
# Stable versus volatile
# ----------------------
# Keys beginning with STABLE_ are written to bazel-out/stable-status.txt, and a
# change to that file invalidates every stamped action that depends on it.
# Every other key goes to bazel-out/volatile-status.txt, which Bazel
# deliberately pretends never changes, so a cache hit can hand back a binary
# carrying a stale value.
#
# That is why the git identity keys here are STABLE_: a new commit or a new
# branch genuinely changes what the artifact is, and a cached binary that
# reports the wrong commit is worse than a cache miss. GIT_DIRTY stays volatile
# because an uncommitted working tree is a local editing state rather than an
# artifact identity; making it stable would invalidate stamped builds on every
# keystroke.
#
# Keys Bazel supplies itself
# --------------------------
# Bazel already emits BUILD_EMBED_LABEL, BUILD_HOST, BUILD_USER,
# BUILD_TIMESTAMP and FORMATTED_DATE, and it owns those files. This script must
# not re-emit them, for two separate reasons:
#
#   * BUILD_TIMESTAMP is epoch seconds. Overriding it with a "%Y%m%d-%H%M%S"
#     string hands every consumer a value it cannot parse.
#   * BUILD_HOST and BUILD_USER belong to stable-status.txt, so they are part of
#     the action cache key. Emitting them again is redundant at best, and a
#     duplicate key, which rule 3 forbids, at worst.
#
# Determinism
# -----------
# Output depends only on the working tree: no timestamps, no hostname, no
# username, no process id. Two runs against an unchanged tree are
# byte-identical. .bazelrc depends on this, and the test asserts it.
# ==============================================================================

set -euo pipefail

# True when a `.git` is reachable. Outside a checkout (an exported source tree,
# a vendored copy) the git keys report `unknown` rather than claiming a clean
# tree at revision none.
if git rev-parse --git-dir >/dev/null 2>&1; then
  in_git_repo=1
else
  in_git_repo=0
fi

# Print one contract-legal line. The substitution collapses newlines and
# carriage returns so that rule 2 holds even if an upstream command produces
# multi-line output.
emit() {
  local key="$1"
  local value="${2-}"
  value="${value//$'\n'/ }"
  value="${value//$'\r'/ }"
  printf '%s %s\n' "$key" "$value"
}

# -----------------------------------------------------------------------------
# STABLE_GIT_COMMIT - full SHA of HEAD, or `unknown`.
# -----------------------------------------------------------------------------
# --verify --quiet is the form that stays silent: plain `git rev-parse HEAD`
# writes "HEAD" to stdout when there is no commit to resolve, which is what made
# this script emit a two-line value.
commit="unknown"
if [ "$in_git_repo" -eq 1 ]; then
  if commit_candidate="$(git rev-parse --verify --quiet HEAD 2>/dev/null)"; then
    commit="${commit_candidate:-unknown}"
  fi
fi
emit "STABLE_GIT_COMMIT" "$commit"

# -----------------------------------------------------------------------------
# STABLE_GIT_BRANCH - branch name, or `detached`.
# -----------------------------------------------------------------------------
# symbolic-ref is used instead of `git rev-parse --abbrev-ref HEAD` because the
# latter returns the literal string "HEAD" in a detached checkout, which is
# indistinguishable from a real branch called HEAD. It also works in a
# repository with no commits, where there is no commit to describe.
branch="unknown"
if [ "$in_git_repo" -eq 1 ]; then
  if branch_candidate="$(git symbolic-ref --quiet --short HEAD 2>/dev/null)"; then
    branch="${branch_candidate:-detached}"
  else
    branch="detached"
  fi
fi
emit "STABLE_GIT_BRANCH" "$branch"

# -----------------------------------------------------------------------------
# GIT_DIRTY (volatile) - `true`, `false` or `unknown`.
# -----------------------------------------------------------------------------
# Untracked files count. An untracked .go file changes what the next build
# produces, so ignoring it would let a cached binary claim to represent a tree
# it was never compiled from.
dirty="unknown"
if [ "$in_git_repo" -eq 1 ]; then
  if [ -n "$(git status --porcelain 2>/dev/null | head -n 1)" ]; then
    dirty="true"
  else
    dirty="false"
  fi
fi
emit "GIT_DIRTY" "$dirty"
