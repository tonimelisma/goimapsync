#!/bin/sh
# Tests for script/process-check's aliases (the former decision rows) against throwaway git
# repositories; the kit's own tests cover the rest. Run by script/verify.
set -u
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0

repo() {
  rm -rf "$work/r"
  mkdir -p "$work/r/script" "$work/r/docs/adr"
  cp "$here/process-check" "$work/r/script/"
  cat >"$work/r/script/process-check.conf" <<'CONF'
id_regex = PV-[0-9]{3}
tests = **/*_test.go
docs = docs/*.md
alias_regex = [DO][0-9]{1,3}
aliases = docs/requirements.md docs/adr/*.md
alias_cited = **/*.go docs/*.md
CONF
  cat >"$work/r/docs/requirements.md" <<'REQ'
# Requirements

- **PV-001** The tool shall back up mail. Aliases: D1, D17. [verified]
- **PV-002** The tool shall back up contacts. Aliases: D17.
  unverified: none yet
REQ
  printf '# 0001. X\n\n- Aliases: D134, O31\n' >"$work/r/docs/adr/0001-x.md"
  printf '// Validates: PV-001\n// Spam is left out (D1), see O31 and D134.\nfunc TestX() {}\n' >"$work/r/x_test.go"
  (cd "$work/r" && git init -q -b main && git add -A && git -c user.email=t@example.com -c user.name=t commit -qm init)
}
run() { (cd "$work/r" && script/process-check) >"$work/out" 2>&1; }
pass() { if run; then echo "ok   $1"; else echo "FAIL $1"; cat "$work/out"; fails=$((fails + 1)); fi; }
fail() {
  if run; then echo "FAIL $1 (passed)"; fails=$((fails + 1))
  elif grep -q -- "$2" "$work/out"; then echo "ok   $1"
  else echo "FAIL $1 (no '$2')"; cat "$work/out"; fails=$((fails + 1)); fi
}

repo
pass "aliases declared in requirements and ADRs resolve"

repo
echo '// Unknown row (D999).' >>"$work/r/x_test.go"
fail "an undeclared alias cited in code fails" "D999 is not an alias"

repo
printf '// Validates: D1\nfunc TestY() {}\n' >>"$work/r/x_test.go"
pass "a Validates tag may name an alias of one requirement"

repo
printf '// Validates: D17\nfunc TestY() {}\n' >>"$work/r/x_test.go"
fail "a Validates tag naming an alias of two requirements fails" "D17 is an alias of PV-001, PV-002"

repo
printf '// Validates: D134\nfunc TestY() {}\n' >>"$work/r/x_test.go"
fail "a Validates tag naming an ADR's alias fails" "D134 is not an alias of any requirement"

[ "$fails" = 0 ] && echo "process-check alias tests: ok" || { echo "process-check alias tests: $fails failed"; exit 1; }
