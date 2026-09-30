#!/bin/bash
# ==============================================================================
# Contract test for workspace-status.sh
# ==============================================================================
# Bazel reads the stdout of --workspace_status_command as a set of key/value
# pairs and does not validate it. Every rule this file checks is therefore
# unenforced by the build itself, and each one that is broken produces correct-
# looking output with wrong values rather than an error.
#
# Run with: bazel test //tools/dev:workspace_status_contract_test
# ==============================================================================

set -uo pipefail

# Path of the script under test, as a runfiles path. Falls back to this script's
# own directory so the test is also runnable outside Bazel.
SCRIPT_LABEL="tools/dev/workspace-status.sh"

locate() {
  local relative="$1"
  local candidate

  if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
    candidate="${TEST_SRCDIR}/${TEST_WORKSPACE}/${relative}"
    if [[ -x "$candidate" ]]; then
      printf '%s' "$candidate"
      return 0
    fi
  fi

  candidate="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "$relative")"
  if [[ -x "$candidate" ]]; then
    printf '%s' "$candidate"
    return 0
  fi

  return 1
}

failures=0

pass() { printf 'ok   - %s\n' "$1"; }

fail() {
  printf 'FAIL - %s\n' "$1" >&2
  failures=$((failures + 1))
}

check() {
  local description="$1"
  shift
  if "$@"; then pass "$description"; else fail "$description"; fi
}

# Asserts that $2 is not one of the newline-separated keys in $1. Used instead of
# a substring match on raw output, which would miss a key that carries a value.
lacks_key() {
  local keys="$1" key="$2"
  ! grep -qx -- "$key" <<<"$keys"
}

value_of() {
  local text="$1" key="$2" line
  while IFS= read -r line; do
    if [[ "$line" == "$key "* ]]; then
      printf '%s' "${line#"$key" }"
      return 0
    fi
  done <<<"$text"
  return 1
}

# -----------------------------------------------------------------------------
# Locate and run
# -----------------------------------------------------------------------------

if ! status_script="$(locate "$SCRIPT_LABEL")"; then
  fail "cannot locate $SCRIPT_LABEL (looked in runfiles and next to this script)"
  exit 1
fi

if ! output="$("$status_script" 2>/dev/null)"; then
  fail "script exited non-zero; Bazel fails the whole build when it does"
  exit 1
fi
pass "script exits 0"

if [[ -n "$output" ]]; then
  pass "script emits at least one key"
else
  fail "script emitted no keys; stamping would silently produce nothing"
  exit 1
fi

# -----------------------------------------------------------------------------
# Rule 2/3: line grammar, uniqueness, no multi-line values
# -----------------------------------------------------------------------------
# Bazel splits on newlines, so a value that spans lines is truncated to its
# first line and the remainder becomes a second, bogus key. Requiring every
# line to match the grammar rules that out.
grammar_violations=""
duplicate_keys=""
while IFS= read -r line; do
  if [[ ! "$line" =~ ^[A-Z_]+\ +[^[:space:]] ]]; then
    grammar_violations+="  bad line: ${line}"$'\n'
  fi
done <<<"$output"

if [[ -z "$grammar_violations" ]]; then
  pass "every line is 'KEY value' with an upper-case key and a non-empty value"
else
  fail "lines do not match the key/value grammar:"$'\n'"$grammar_violations"
fi

# Duplicates are forbidden. This also catches a value that leaked a newline and
# masqueraded as a second key, which is how the old script produced a bogus
# STABLE_GIT_COMMIT.
keys="$(sed -E 's/^([A-Z_]+) .*$/\1/' <<<"$output" | sort)"
duplicates="$(uniq -d <<<"$keys")"
if [[ -z "$duplicates" ]]; then
  pass "no duplicate keys"
else
  fail "duplicate keys: $(tr '\n' ' ' <<<"$duplicates")"
fi

# No carriage returns: a CR would be part of the value and break anything that
# compares against a git SHA.
if [[ "$output" != *$'\r'* ]]; then
  pass "output contains no carriage returns"
else
  fail "output contains carriage returns, which become part of the value"
fi

# -----------------------------------------------------------------------------
# Key set
# -----------------------------------------------------------------------------
# Pinned exactly, so adding a key is a deliberate act rather than a side effect
# of editing the script.
expected_keys="GIT_DIRTY
STABLE_GIT_BRANCH
STABLE_GIT_COMMIT"
if [[ "$keys" == "$expected_keys" ]]; then
  pass "emits exactly the expected keys"
else
  fail "unexpected key set.
expected:
$expected_keys
actual:
$keys"
fi

# -----------------------------------------------------------------------------
# Stable versus volatile classification
# -----------------------------------------------------------------------------
# A volatile key can serve a stale value out of the action cache, so the keys
# that determine artifact identity must be STABLE_ prefixed.
commit_key="$(grep -oE '^[A-Z_]*GIT_COMMIT' <<<"$output" || true)"
branch_key="$(grep -oE '^[A-Z_]*GIT_BRANCH' <<<"$output" || true)"

check "the commit key is STABLE_ prefixed" test "$commit_key" = "STABLE_GIT_COMMIT"
check "the branch key is STABLE_ prefixed" test "$branch_key" = "STABLE_GIT_BRANCH"

# GIT_DIRTY tracks an uncommitted working tree, which is an editing state rather
# than an artifact identity. Stable would invalidate stamped builds constantly.
dirty_key="$(grep -oE '^[A-Z_]*GIT_DIRTY' <<<"$output" || true)"
check "GIT_DIRTY is volatile" test "$dirty_key" = "GIT_DIRTY"

# -----------------------------------------------------------------------------
# Bazel's built-in keys are not re-emitted
# -----------------------------------------------------------------------------
# Bazel owns these and writes them itself. Re-emitting duplicates a key, and in
# the case of BUILD_TIMESTAMP replaces epoch seconds with a formatted string that
# no consumer can parse.
#
# The comparison is against the extracted key list rather than the raw output:
# matching the text "BUILD_HOST " would only catch a line that is *exactly* that
# string, so a script emitting "BUILD_HOST archlinux" would slip through.
for builtin in BUILD_EMBED_LABEL BUILD_HOST BUILD_USER BUILD_TIMESTAMP FORMATTED_DATE; do
  check "does not re-emit Bazel's built-in $builtin" lacks_key "$keys" "$builtin"
done

# -----------------------------------------------------------------------------
# Values
# -----------------------------------------------------------------------------
commit="$(value_of "$output" "STABLE_GIT_COMMIT" || true)"
if [[ "$commit" =~ ^[0-9a-f]{40}$ || "$commit" == "unknown" ]]; then
  pass "STABLE_GIT_COMMIT is a 40-character SHA or 'unknown' (got '$commit')"
else
  fail "STABLE_GIT_COMMIT must be a 40-character SHA or 'unknown', got '$commit'."
  echo "      The literal string 'HEAD' here means git printed HEAD to stdout and" >&2
  echo "      exited non-zero, so the old \$(git rev-parse HEAD || echo unknown)" >&2
  echo "      captured two lines and stamped a two-line value." >&2
fi

branch="$(value_of "$output" "STABLE_GIT_BRANCH" || true)"
if [[ "$branch" == "HEAD" ]]; then
  fail "STABLE_GIT_BRANCH is the literal 'HEAD', which is what"
  echo "      'git rev-parse --abbrev-ref HEAD' returns in a detached checkout." >&2
elif [[ "$branch" =~ ^[A-Za-z0-9._/-]+$ || "$branch" == "detached" || "$branch" == "unknown" ]]; then
  pass "STABLE_GIT_BRANCH is a ref name, 'detached' or 'unknown' (got '$branch')"
else
  fail "STABLE_GIT_BRANCH must be a ref name, 'detached' or 'unknown', got '$branch'"
fi

dirty="$(value_of "$output" "GIT_DIRTY" || true)"
check "GIT_DIRTY is true, false or unknown" test -n "$(printf '%s' "$dirty" | grep -Ex 'true|false|unknown')"

# -----------------------------------------------------------------------------
# Determinism
# -----------------------------------------------------------------------------
# .bazelrc turns stamping on and this file is the only thing standing between a
# keystroke and a different build identity. Two runs against an unchanged tree
# must be byte-identical.
second="$("$status_script" 2>/dev/null)"
check "output is deterministic across runs" test "$output" = "$second"

# -----------------------------------------------------------------------------

if (( failures > 0 )); then
  printf '\n%d check(s) failed.\n' "$failures" >&2
  exit 1
fi

printf '\nAll workspace status contract checks passed.\n'
