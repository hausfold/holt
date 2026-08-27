#!/usr/bin/env bats
# Hermetic tests for `wt` — the agent-worktree manager (modules/den/wt.sh).
#
# Everything runs against throwaway repos in $BATS_TEST_TMPDIR: HOME, the
# worktree base (CLAUDE_WT_BASE, already an env knob) and every external tool
# wt shells out to (`gh`, `lsof`) are substituted, so the suite never touches
# the machine's real registry, real repos, or the network. `wt`'s bare-PATH
# rescue is APPENDED rather than prepended precisely so these shims win.
#
# What this pins down, roughly in the order a worktree lives:
#   create → park/unpark → list → resume → remove → reap → registry upkeep
#
# Nothing here is skipped — every contract the suite states holds. To see what a
# given revision of the script breaks, point the suite at it:
#
#   git show <rev>:modules/den/wt.sh > /tmp/wt.sh
#   WT_UNDER_TEST=/tmp/wt.sh bats test/wt.bats
#
# That is the intended way to demonstrate a bug: write the test, watch it fail
# against the old copy, fix, watch it pass against both the new copy and the
# whole suite. A test that passes against BOTH copies is not reproducing the bug
# you think it is — two of these did exactly that on the first attempt.

bats_require_minimum_version 1.5.0   # `run --separate-stderr`, used by the width test

setup() {
  # WT_UNDER_TEST lets you point the whole suite at another copy of the script —
  # an older revision, a candidate rewrite — to see exactly which contracts it
  # breaks. That is how the fixes below were shown to fix something.
  WT="${WT_UNDER_TEST:-$BATS_TEST_DIRNAME/../holt}"
  # Fail LOUDLY when there is no binary to test, instead of letting every
  # invocation exit 127 and every fixture capture an empty path. That is not a
  # theoretical tidiness: with $WT missing, `dir="$(hook_create …)"` yields "",
  # `commit_in` then runs `git -C "" commit`, and `git -C ""` means THE CURRENT
  # DIRECTORY — so the suite committed twice into the real holt checkout it was
  # being run from. `make test` builds first; running bats directly does not.
  [ -x "$WT" ] || {
    printf 'no holt binary at %s — run `make test`, or set WT_UNDER_TEST\n' "$WT" >&2
    return 1
  }
  # macOS puts BATS_TEST_TMPDIR under /var/folders, a symlink to /private/var.
  # git resolves paths (`rev-parse --path-format=absolute`) while our fixtures
  # would carry the unresolved form, so registry rows and git's own answers
  # would never string-compare equal. Resolve once, up front.
  TMP="$(cd "$BATS_TEST_TMPDIR" && pwd -P)"

  # Hermetic git: the machine's global config (gpgsign, hooks, default branch,
  # user identity) must not leak in or the same test passes here and fails in CI.
  export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
  export GIT_AUTHOR_NAME=Test GIT_AUTHOR_EMAIL=t@example.com
  export GIT_COMMITTER_NAME=Test GIT_COMMITTER_EMAIL=t@example.com

  export HOME="$TMP/home"                 # wt_projdir + the WT_BASE default live here
  export XDG_CONFIG_HOME="$TMP/config"    # Holt's persisted default lives here
  # Occupancy leases live under here. NOT $TMP/state — the hook tests use that
  # path as a scratch FILE to record that they ran, and a directory of the same
  # name makes their `cat` fail in a way that reads as a hook bug.
  export XDG_STATE_HOME="$TMP/xdg-state"
  unset HOLT_AGENT HAUS_AGENT_DEFAULT # machine choices must not leak into the fixture
  unset HOLT_STATE HOLT_OCCUPANCY          # ditto — the lease dir and its sole-provider switch
  unset HOLT_TRILL                         # ditto — the notify tests shim trill on PATH
  export CLAUDE_WT_BASE="$TMP/wtbase"
  REG="$CLAUDE_WT_BASE/registry.tsv"
  mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME"

  BIN="$TMP/bin"; mkdir -p "$BIN"
  export PATH="$BIN:$PATH"

  # ── shim: gh ───────────────────────────────────────────────────────────────
  # wt asks gh four things, and the shim answers by shape:
  #   --head <branch>  the precise "did THIS branch's PR merge, at what SHA?"
  #                    (pr_merge_info, the gate branch_landed reaps on).
  #                    FAKE_GH_MERGED=1 → yes, with FAKE_GH_OID/FAKE_GH_PR.
  #   no --head        the repo-wide merged-PR map (merged_map — one call per
  #                    repo, feeds the +N annotations). Answers for the single
  #                    branch FAKE_GH_BRANCH, which is all any test needs.
  #   --state open     two shapes, told apart by the fields asked for:
  #                    --json url  → "is a PR already open?" (reship) →
  #                    FAKE_GH_OPEN_URL. --json …headRefOid → the repo-wide
  #                    OPEN map (open_map, the other half of the +N answer) →
  #                    FAKE_GH_OPEN_BRANCH/_OID/_PR.
  #   pr create        opens one → FAKE_GH_PR_URL
  # Printing nothing is a real gh's answer when offline or unauthenticated.
  cat >"$BIN/gh" <<'EOF'
#!/usr/bin/env bash
printf 'gh %s\n' "$*" >>"${FAKE_GH_LOG:-/dev/null}"
case "$1 $2" in
  "pr create") printf '%s\n' "${FAKE_GH_PR_URL:-https://github.com/acme/alpha/pull/9}"; exit 0 ;;
esac
case "$1 $2" in
  "repo view") printf '%s' "${FAKE_GH_ARCHIVED:-false}"; exit 0 ;;
esac
case " $* " in
  *" --state open "*)
    case " $* " in
      *headRefOid*)
        [ -n "${FAKE_GH_OPEN_BRANCH:-}" ] || exit 0
        printf '%s\t%s\t%s\n' "$FAKE_GH_OPEN_BRANCH" "${FAKE_GH_OPEN_OID:-}" "${FAKE_GH_OPEN_PR:-8}"
        exit 0 ;;
    esac
    printf '%s' "${FAKE_GH_OPEN_URL:-}"; exit 0 ;;
  *" --state closed "*) printf '%s' "${FAKE_GH_CLOSED_PR:-}"; exit 0 ;;
  *" --head "*)
    [ "${FAKE_GH_MERGED:-0}" = 1 ] || exit 0
    printf 'MERGED %s %s\n' "${FAKE_GH_OID:-}" "${FAKE_GH_PR:-7}"; exit 0 ;;
esac
[ "${FAKE_GH_MERGED:-0}" = 1 ] && [ -n "${FAKE_GH_BRANCH:-}" ] || exit 0
printf '%s\t%s\t%s\n' "$FAKE_GH_BRANCH" "${FAKE_GH_OID:-}" "${FAKE_GH_PR:-7}"
EOF

  # ── shim: lsof ─────────────────────────────────────────────────────────────
  # wt reads one dump of every process's cwd to decide "is a pane standing in
  # this worktree?". Always emit at least "/" so the dump is non-empty — an
  # EMPTY dump is wt's "lsof told me nothing, degrade to parked-only" signal,
  # which FAKE_LSOF_BROKEN=1 exercises deliberately.
  #
  # Real `-F pcn` FIELD SETS, not bare `n` lines: holt now carries the pid and
  # command name into every refusal it prints, so a shim that emitted only
  # paths would let the parse regress without a single test noticing. Pids
  # start at 4001 and count up per cwd; FAKE_LSOF_CMD names them all.
  cat >"$BIN/lsof" <<'EOF'
#!/usr/bin/env bash
[ "${FAKE_LSOF_BROKEN:-0}" = 1 ] && exit 1
printf 'p1\ncinit\nfcwd\nn/\n'
pid=4000
for c in ${FAKE_LSOF_CWDS:-}; do
  pid=$((pid + 1))
  printf 'p%s\nc%s\nfcwd\nn%s\n' "$pid" "${FAKE_LSOF_CMD:-zsh}" "$c"
done
EOF

  chmod +x "$BIN/gh" "$BIN/lsof"
  export FAKE_GH_LOG="$TMP/gh.log"
}

# ── fixtures ─────────────────────────────────────────────────────────────────

mkrepo() { # mkrepo <name> — a main checkout on `main`, with a GitHub origin
  local name="$1" main="$TMP/repos/$1"
  mkdir -p "$main"
  git -C "$main" init -q -b main
  git -C "$main" config commit.gpgsign false
  echo hello >"$main/README.md"
  git -C "$main" add -A
  git -C "$main" commit -qm init
  # repo_slug parses this for `gh -R`; a real-looking URL keeps that path honest.
  git -C "$main" remote add origin "https://github.com/acme/$name.git"
  printf '%s' "$main"
}

wt_run() { run "$WT" "$@"; }

hook_create() { # hook_create <main> <name> — drive the WorktreeCreate hook
  printf '{"name":"%s","cwd":"%s"}' "$2" "$1" | "$WT" create
}

hook_remove() { # hook_remove <worktree-path>
  printf '{"worktree_path":"%s"}' "$1" | "$WT" remove
}

commit_in() { # commit_in <checkout> <file> <msg> — give a branch real history
  # Guard the path. `git -C ""` is not an error to git — it means the current
  # directory — so an empty $1 here silently retargets every command below at
  # whatever repo the suite is being RUN from. That is not hypothetical: it
  # happened, and it left two commits in the real holt repo.
  [ -n "$1" ] && [ -d "$1" ] || { printf 'commit_in: refusing an empty/missing checkout path\n' >&2; return 1; }
  echo "$RANDOM" >"$1/$2"
  git -C "$1" add -A
  git -C "$1" -c commit.gpgsign=false commit -qm "$3"
}

# A worktree with one commit of its own, so it is NOT ancestry-merged into main
# and therefore survives the self-heal sweep that every `wt` listing runs.
mkwt() { # mkwt <main> <name> — echo the checkout path
  local dir; dir="$(hook_create "$1" "$2")"
  [ -n "$dir" ] && [ -d "$dir" ] || { printf 'mkwt: create gave no usable path (%s)\n' "$dir" >&2; return 1; }
  commit_in "$dir" work.txt "work on $2"
  printf '%s' "$dir"
}

# awk, not `grep -c`: grep prints "0" AND exits 1 on an empty file, so the
# obvious `grep -c . "$REG" || echo 0` emits "0\n0" and every -eq blows up.
reg_rows() { awk 'NF' "$REG" 2>/dev/null | wc -l | tr -d ' '; }

fail() { printf '%s\n' "$*" >&2; return 1; }   # not a bats builtin

# ── create (WorktreeCreate hook) ─────────────────────────────────────────────

@test "create: makes <base>/<name> on worktree-<name> and prints ONLY the path" {
  local main; main="$(mkrepo alpha)"
  run bash -c "printf '{\"name\":\"sparkle\",\"cwd\":\"$main\"}' | '$WT' create 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$output" = "$CLAUDE_WT_BASE/alpha/sparkle" ]
  [ -e "$output/.git" ]
  [ "$(git -C "$output" branch --show-current)" = worktree-sparkle ]
}

@test "create: accepts the documented key names too (worktree_name/base_path)" {
  local main; main="$(mkrepo alpha)"
  run bash -c "printf '{\"worktree_name\":\"doc\",\"base_path\":\"$main\"}' | '$WT' create 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$(git -C "$output" branch --show-current)" = worktree-doc ]
}

@test "create: records main, branch, path, parent, and its Claude client in the registry" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  run cat "$REG"
  [ "$output" = "$(printf 'sparkle\t%s\tworktree-sparkle\t%s\t%s\tclaude' "$main" "$dir" "$main")" ]
}

@test "create: a name whose branch already exists fails instead of half-creating" {
  local main; main="$(mkrepo alpha)"
  hook_create "$main" dup >/dev/null 2>&1
  rm -rf "$CLAUDE_WT_BASE/alpha/dup"
  run bash -c "printf '{\"name\":\"dup\",\"cwd\":\"$main\"}' | '$WT' create"
  [ "$status" -ne 0 ]
  # NOTE: today this is a raw `git worktree add` error. cmd_child has friendly
  # collision guards; cmd_create does not. See the create-guard gap.
}

@test "create: a garbage hook payload fails loudly, naming the keys it wanted" {
  run bash -c "printf '{\"nope\":1}' | '$WT' create"
  [ "$status" -ne 0 ]
  [[ "$output" == *"none of"* ]]
}

# ── park ─────────────────────────────────────────────────────────────────────

@test "park: dirty tree becomes one wip: commit and the tree goes clean" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" p1)"
  echo edited >"$dir/README.md"
  echo new >"$dir/untracked.txt"
  cd "$dir"; wt_run park "mid refactor"
  [ "$status" -eq 0 ]
  [[ "$output" == *"parked 2 change(s)"* ]]
  [ -z "$(git -C "$dir" status --porcelain)" ]
  [[ "$(git -C "$dir" log -1 --format=%s)" == "wip: mid refactor (parked "* ]]
  # Untracked files are swept in too — that is the point of "set the tree aside".
  git -C "$dir" show --name-only --format= HEAD | grep -qx untracked.txt
}

@test "park: with no label still parks, under a generic subject" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" p2)"
  echo x >>"$dir/README.md"
  cd "$dir"; wt_run park
  [ "$status" -eq 0 ]
  [[ "$(git -C "$dir" log -1 --format=%s)" == "wip: parked "* ]]
}

@test "park: a clean tree is a no-op, not an empty commit" {
  local main dir head; main="$(mkrepo alpha)"; dir="$(mkwt "$main" p3)"
  head="$(git -C "$dir" rev-parse HEAD)"
  cd "$dir"; wt_run park
  [ "$status" -eq 0 ]
  [[ "$output" == *"nothing to park"* ]]
  [ "$(git -C "$dir" rev-parse HEAD)" = "$head" ]
}

@test "park: refuses on detached HEAD — the commit would be unreachable" {
  local main dir head; main="$(mkrepo alpha)"; dir="$(mkwt "$main" p4)"
  git -C "$dir" checkout -q --detach
  head="$(git -C "$dir" rev-parse HEAD)"
  echo x >>"$dir/README.md"
  cd "$dir"; wt_run park
  [ "$status" -ne 0 ]
  [[ "$output" == *"detached"* ]]
  [ "$(git -C "$dir" rev-parse HEAD)" = "$head" ]
  [ -n "$(git -C "$dir" status --porcelain)" ]   # the edit is untouched
}

@test "park: on a non-agent branch it still parks, but warns not to push it" {
  local main; main="$(mkrepo alpha)"
  echo x >>"$main/README.md"
  cd "$main"; wt_run park
  [ "$status" -eq 0 ]
  [[ "$output" == *"isn't an agent branch"* ]]
}

@test "park: outside a git repo dies without touching anything" {
  mkdir -p "$TMP/notarepo"; cd "$TMP/notarepo"
  wt_run park
  [ "$status" -ne 0 ]
  [[ "$output" == *"not in a git repo"* ]]
}

# ── unpark ───────────────────────────────────────────────────────────────────

@test "unpark: rewinds the wip commit and gives the files back, uncommitted" {
  local main dir base; main="$(mkrepo alpha)"; dir="$(mkwt "$main" u1)"
  base="$(git -C "$dir" rev-parse HEAD)"
  echo edited >"$dir/README.md"
  cd "$dir"; "$WT" park >/dev/null 2>&1
  wt_run unpark
  [ "$status" -eq 0 ]
  [ "$(git -C "$dir" rev-parse HEAD)" = "$base" ]
  [ "$(cat "$dir/README.md")" = edited ]
  [ -n "$(git -C "$dir" status --porcelain)" ]
}

@test "unpark: a parked UNTRACKED file comes back untracked, not staged" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" u2)"
  echo new >"$dir/fresh.txt"
  cd "$dir"; "$WT" park >/dev/null 2>&1
  wt_run unpark
  [ "$status" -eq 0 ]
  [ "$(git -C "$dir" status --porcelain fresh.txt)" = "?? fresh.txt" ]
}

@test "unpark: refuses when HEAD isn't a wip: commit" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" u3)"
  cd "$dir"; wt_run unpark
  [ "$status" -ne 0 ]
  [[ "$output" == *"isn't a parked commit"* ]]
}

@test "unpark: refuses to rewrite a wip commit that is already pushed" {
  local main dir head; main="$(mkrepo alpha)"; dir="$(mkwt "$main" u4)"
  echo edited >"$dir/README.md"
  cd "$dir"; "$WT" park >/dev/null 2>&1
  head="$(git -C "$dir" rev-parse HEAD)"
  # Stand in for "pushed": a remote-tracking ref that contains the wip commit.
  git -C "$dir" update-ref refs/remotes/origin/worktree-u4 HEAD
  wt_run unpark
  [ "$status" -ne 0 ]
  [[ "$output" == *"already pushed"* ]]
  [ "$(git -C "$dir" rev-parse HEAD)" = "$head" ]   # never force-push behind your back
}

@test "unpark: refuses when the wip commit is the branch's root commit" {
  local root; root="$TMP/repos/rootonly"
  mkdir -p "$root"; git -C "$root" init -q -b main
  git -C "$root" config commit.gpgsign false
  echo a >"$root/a.txt"
  cd "$root"; "$WT" park >/dev/null 2>&1
  wt_run unpark
  [ "$status" -ne 0 ]
  [[ "$output" == *"first commit"* ]]
}

@test "unpark: two parks need two unparks — one call rewinds only the newest" {
  local main dir base; main="$(mkrepo alpha)"; dir="$(mkwt "$main" u5)"
  base="$(git -C "$dir" rev-parse HEAD)"
  cd "$dir"
  echo one >"$dir/one.txt";  "$WT" park first  >/dev/null 2>&1
  echo two >"$dir/two.txt";  "$WT" park second >/dev/null 2>&1
  "$WT" unpark >/dev/null 2>&1
  [ "$(git -C "$dir" rev-parse HEAD)" != "$base" ]        # the first park is still committed
  [[ "$(git -C "$dir" log -1 --format=%s)" == "wip: first"* ]]
  "$WT" unpark >/dev/null 2>&1
  [ "$(git -C "$dir" rev-parse HEAD)" = "$base" ]
  [ -f "$dir/one.txt" ] && [ -f "$dir/two.txt" ]
}

# ── list ─────────────────────────────────────────────────────────────────────

@test "list: says so plainly when there is nothing parked" {
  wt_run list
  [ "$status" -eq 0 ]
  [[ "$output" == *"none parked"* ]]
}

@test "list: shows a live checkout as live and a removed one as parked" {
  local main a b; main="$(mkrepo alpha)"
  a="$(mkwt "$main" alive)"; b="$(mkwt "$main" gone)"
  git -C "$main" worktree remove --force "$b"
  wt_run list
  [ "$status" -eq 0 ]
  [[ "$output" == *"alive"*"live"* ]]
  echo "$output" | grep -Eq '^\s+alpha\s+gone\s+parked'
}

# An agent branch can be checked out somewhere `wt` never put it: a raw
# `git worktree add`, or another agent's own worktree feature (codex keeps its
# under ~/.codex/worktrees/). Such a branch is only ever discovered by the
# orphan scan, which SYNTHESIZES a path from the bucket convention — a path that
# does not exist. Trusting that guess files a very-much-live checkout as parked.
# (A rename INSIDE the base is not this case: the disk glob re-reads each live
# checkout's current branch, so it self-corrects.)
mk_stray() { # mk_stray <main> <name> — a worktree-<name> checkout outside WT_BASE
  local out="$TMP/elsewhere/$2"
  mkdir -p "$TMP/elsewhere"
  # wt discovers repos through the registry and the WT_BASE glob, so a repo whose
  # ONLY worktree is a stray is invisible entirely — a different (and correct)
  # behaviour. Anchor the repo with one ordinary worktree so the orphan scan runs
  # and the stray is actually reached. That is also the real-world shape.
  [ -n "$(awk -F'\t' -v m="$1" '$2==m' "$REG" 2>/dev/null)" ] || mkwt "$1" "${2}-anchor" >/dev/null
  git -C "$1" worktree add -q -b "worktree-$2" "$out" >/dev/null 2>&1
  commit_in "$out" stray.txt "work in an unregistered checkout"
  printf '%s' "$out"
}

@test "list: a branch checked out OUTSIDE the worktree base still reads as live" {
  local main out; main="$(mkrepo alpha)"; out="$(mk_stray "$main" manual)"
  wt_run list
  [ "$status" -eq 0 ]
  echo "$output" | grep -Eq '^\s+alpha\s+manual\s+live'
  [ -e "$out/.git" ]
}

@test "list: stays one line per worktree in a narrow pane" {
  local main; main="$(mkrepo alpha)"
  mkwt "$main" a-rather-long-worktree-name >/dev/null
  # stdout is the table; stderr is `say`'s banner, which is a fixed sentence and
  # is allowed to be wider than the pane. Only the table has a width contract.
  COLUMNS=48 run --separate-stderr "$WT" list
  [ "$status" -eq 0 ]
  while IFS= read -r l; do
    [ "${#l}" -le 48 ] || fail "table line wider than COLUMNS=48: $l"
  done <<<"$(printf '%s' "$output" | sed $'s/\033\\[[0-9;]*m//g')"
}

@test "list: self-heals — a parked branch already merged into main is reaped" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" landed)"
  git -C "$main" merge -q --no-edit worktree-landed
  git -C "$main" worktree remove --force "$dir"
  wt_run list
  [ "$status" -eq 0 ]
  [[ "$output" == *"swept 1 merged lane"* ]]
  run git -C "$main" show-ref -q --verify refs/heads/worktree-landed
  [ "$status" -ne 0 ]
}

# ── resume ───────────────────────────────────────────────────────────────────

@test "resume: rebuilds a parked checkout at its registered path" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" back)"
  git -C "$main" worktree remove --force "$dir"
  [ ! -e "$dir" ]
  wt_run resume back
  [ "$status" -eq 0 ]
  [ -e "$dir/.git" ]
  [ "$(git -C "$dir" branch --show-current)" = worktree-back ]
  [[ "$output" == *"claude --continue"* ]]   # no tty → prints the command, never execs
}

@test "resume: a lane's own chat is CONTINUED, never offered as a picker" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" solo)"
  git -C "$main" worktree remove --force "$dir"
  wt_run resume solo
  [ "$status" -eq 0 ]
  # One checkout, one lane, one newest conversation: asking which is a question
  # with a single answer, and holt is the one holding it.
  [[ "$output" == *"claude --continue"* ]]
  [[ "$output" != *"--resume"* ]] || fail "the picker came back: $output"
}

@test "resume: --pick asks for the picker anyway, either side of the name" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" choosy)"
  git -C "$main" worktree remove --force "$dir"
  wt_run resume choosy --pick
  [ "$status" -eq 0 ]
  [[ "$output" == *"claude --resume"* ]]
  wt_run resume --pick choosy
  [ "$status" -eq 0 ]
  [[ "$output" == *"claude --resume"* ]]
}

@test "resume: a live worktree is reported live, not rebuilt" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" here)"
  wt_run resume here
  [ "$status" -eq 0 ]
  [[ "$output" == *"still live at $dir"* ]]
}

@test "resume: an ambiguous name across two repos demands a repo qualifier" {
  local a b; a="$(mkrepo alpha)"; b="$(mkrepo beta)"
  mkwt "$a" twin >/dev/null; mkwt "$b" twin >/dev/null
  wt_run resume twin
  [ "$status" -ne 0 ]
  [[ "$output" == *"more than one repo"* ]]
  wt_run resume alpha/twin
  [ "$status" -eq 0 ]
}

@test "resume: an unknown name dies pointing at the listing" {
  wt_run resume nope
  [ "$status" -ne 0 ]
  [[ "$output" == *"no lane named 'nope'"* ]]
}

# ── focus ────────────────────────────────────────────────────────────────────

@test "focus: the hook is handed the lane, and its yes ends it" {
  local main hook; main="$(mkrepo alpha)"; mkwt "$main" glance >/dev/null
  hook="$(mkhook focus 'printf "%s %s\n" "$HOLT_NAME" "$HOLT_MAIN" >"'"$TMP"'/focused"; exit 0')"
  setcfg "[hooks]
focus = \"$hook\""
  wt_run focus glance
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/focused")" = "glance $main" ]
  # Handled means handled: resume must not also run and open a second window
  # onto the session the hook just raised.
  [[ "$output" != *"claude --continue"* ]] || fail "resume ran behind the hook: $output"
}

@test "focus: a hook that defers falls back to resume — a lane with no window still opens one" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" detached)"
  git -C "$main" worktree remove --force "$dir"
  hook="$(mkhook focus 'exit 3')"
  setcfg "[hooks]
focus = \"$hook\""
  wt_run focus detached
  [ "$status" -eq 0 ]
  [ -e "$dir/.git" ]
  [[ "$output" == *"claude --continue"* ]]
}

@test "focus: with no hook at all it is resume — holt's own answer to go-to-this-lane" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" plain)"
  git -C "$main" worktree remove --force "$dir"
  wt_run focus plain
  [ "$status" -eq 0 ]
  [[ "$output" == *"claude --continue"* ]]
}

@test "focus: an ambiguous or unknown name is refused, never guessed at" {
  local a b; a="$(mkrepo alpha)"; b="$(mkrepo beta)"
  mkwt "$a" twin >/dev/null; mkwt "$b" twin >/dev/null
  wt_run focus twin
  [ "$status" -ne 0 ]
  [[ "$output" == *"more than one repo"* ]]
  wt_run focus nope
  [ "$status" -ne 0 ]
  [[ "$output" == *"no lane named 'nope'"* ]]
  wt_run focus
  [ "$status" -ne 0 ]
}

@test "resume: a branch checked out OUTSIDE the base is reported live, not re-added" {
  local main out; main="$(mkrepo alpha)"; out="$(mk_stray "$main" manual)"
  wt_run resume manual
  [ "$status" -eq 0 ]
  # Trusting the synthesized path here means `git worktree add` on a branch that
  # is already checked out — which fails outright, so the worktree is unreachable.
  [[ "$output" == *"still live at $out"* ]]
}

# ── remove (WorktreeRemove hook) ─────────────────────────────────────────────

@test "remove: unmerged work survives — checkout gone, branch and registry kept" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" keep)"
  hook_remove "$dir"
  [ ! -e "$dir" ]
  git -C "$main" show-ref -q --verify refs/heads/worktree-keep
  [ "$(reg_rows)" -eq 1 ]
}

@test "remove: uncommitted edits are auto-parked as a wip commit, never dropped" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" dirty)"
  echo precious >"$dir/README.md"
  hook_remove "$dir"
  [[ "$(git -C "$main" log -1 --format=%s worktree-dirty)" == "wip: auto-saved on pane close"* ]]
  [ "$(git -C "$main" show worktree-dirty:README.md)" = precious ]
}

@test "remove: an ancestry-merged branch is reaped and its registry row dropped" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" done)"
  git -C "$main" merge -q --no-edit worktree-done
  hook_remove "$dir"
  run git -C "$main" show-ref -q --verify refs/heads/worktree-done
  [ "$status" -ne 0 ]
  [ "$(reg_rows)" -eq 0 ]
}

@test "remove: landed branch with ONLY untracked scratch reaps instead of parking" {
  # The regression that made merged worktrees pile up: WIP-committing build
  # scratch moves the tip past the merged PR's SHA, so the merge stops being
  # recognized and the worktree is falsely parked forever.
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" scratch)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)"
  mkdir -p "$dir/target"; echo junk >"$dir/target/o.o"
  hook_remove "$dir"
  run git -C "$main" show-ref -q --verify refs/heads/worktree-scratch
  [ "$status" -ne 0 ]
}

@test "remove: landed branch with TRACKED edits parks them and keeps the branch" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" late)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)"
  echo after-the-merge >"$dir/README.md"
  hook_remove "$dir"
  git -C "$main" show-ref -q --verify refs/heads/worktree-late
  [[ "$(git -C "$main" log -1 --format=%s worktree-late)" == "wip: auto-saved"* ]]
}

@test "remove: a squash-merged branch whose tip moved on is NOT reaped" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" moved)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)"
  commit_in "$dir" post.txt "work done after the PR merged"   # tip != headRefOid
  hook_remove "$dir"
  git -C "$main" show-ref -q --verify refs/heads/worktree-moved
}

# ── notify (Notification/Stop hook) ──────────────────────────────────────────
# The one hook that changes nothing: it forwards "blocked on the user" /
# "finished the turn" to trill as a banner. Its contract is exit 0 ALWAYS — a
# hook that can fail is a hook that can break the session it watches.

mktrill() { # a recording trill shim on PATH; FAKE_TRILL_EXIT fakes the daemon
  cat >"$BIN/trill" <<'EOF'
#!/usr/bin/env bash
printf 'trill %s\n' "$*" >>"${FAKE_TRILL_LOG:-/dev/null}"
exit "${FAKE_TRILL_EXIT:-0}"
EOF
  chmod +x "$BIN/trill"
  export FAKE_TRILL_LOG="$TMP/trill.log"
}

hook_notify() { # hook_notify <json> — drive the notify hook
  printf '%s' "$1" | "$WT" hook notify
}

@test "notify: a Notification becomes an ask banner titled with the lane name" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill
  run hook_notify "{\"hook_event_name\":\"Notification\",\"cwd\":\"$dir\",\"message\":\"needs permission to use Bash\"}"
  [ "$status" -eq 0 ]
  grep -q -- '--kind ask' "$FAKE_TRILL_LOG"
  grep -q -- '--title sparkle' "$FAKE_TRILL_LOG"
  # The payload message is conversation content — it must never reach trill.
  ! grep -q 'permission' "$FAKE_TRILL_LOG"
}

@test "notify: a Stop becomes a done banner" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill
  run hook_notify "{\"hook_event_name\":\"Stop\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
  grep -q -- '--kind done' "$FAKE_TRILL_LOG"
  grep -q -- '--title sparkle' "$FAKE_TRILL_LOG"
}

@test "notify: trill exit 2 (daemon down) is swallowed — the hook still exits 0" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill; export FAKE_TRILL_EXIT=2
  run hook_notify "{\"hook_event_name\":\"Stop\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
}

@test "notify: no trill binary anywhere is a silent no-op, exit 0" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  export HOLT_TRILL="$TMP/no-such-binary"   # authoritative when set: no fall-through
  run hook_notify "{\"hook_event_name\":\"Stop\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "notify: a garbage payload exits 0 — this hook must never break a session" {
  mktrill
  run hook_notify 'not json at all'
  [ "$status" -eq 0 ]
  run hook_notify '{"hook_event_name":"SomethingNew","cwd":"/x"}'
  [ "$status" -eq 0 ]
  ! grep -q 'trill' "$FAKE_TRILL_LOG"
}

@test "notify: a Notification is keyed by lane, so the next one replaces the fin" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill
  run hook_notify "{\"hook_event_name\":\"Notification\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
  grep -q -- '--key holt/alpha/sparkle' "$FAKE_TRILL_LOG"
}

# The other half of the ask: the session moved again, so the question its fin
# asks has been answered. UserPromptSubmit is the user typing; PostToolUse means
# a tool actually ran, which is what approving a permission prompt leads to.
@test "notify: a resume event resolves the lane's fin" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill
  hook_notify "{\"hook_event_name\":\"Notification\",\"cwd\":\"$dir\"}"
  : >"$FAKE_TRILL_LOG"
  run hook_notify "{\"hook_event_name\":\"PostToolUse\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
  grep -q -- 'trill resolve holt/alpha/sparkle' "$FAKE_TRILL_LOG"
}

# PostToolUse fires on every tool call in every pane. With no fin outstanding it
# must not launch trill at all — that binary is Trill.app's, and paying for it
# per tool call is the whole reason the marker gate exists.
@test "notify: a resume event with nothing outstanding launches no trill" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill
  run hook_notify "{\"hook_event_name\":\"UserPromptSubmit\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
  [ ! -s "$FAKE_TRILL_LOG" ]
}

# One resolve, not one per tool call: the fin is down after the first.
@test "notify: the fin resolves once, then the gate is shut again" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill
  hook_notify "{\"hook_event_name\":\"Notification\",\"cwd\":\"$dir\"}"
  hook_notify "{\"hook_event_name\":\"PostToolUse\",\"cwd\":\"$dir\"}"
  : >"$FAKE_TRILL_LOG"
  run hook_notify "{\"hook_event_name\":\"PostToolUse\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
  [ ! -s "$FAKE_TRILL_LOG" ]
}

# Another lane moving is not this lane being answered.
@test "notify: a resume event from a different lane leaves the fin up" {
  local main a b; main="$(mkrepo alpha)"
  a="$(hook_create "$main" sparkle)"; b="$(hook_create "$main" other)"
  mktrill
  hook_notify "{\"hook_event_name\":\"Notification\",\"cwd\":\"$a\"}"
  : >"$FAKE_TRILL_LOG"
  run hook_notify "{\"hook_event_name\":\"PostToolUse\",\"cwd\":\"$b\"}"
  [ "$status" -eq 0 ]
  [ ! -s "$FAKE_TRILL_LOG" ]
  # …and the fin is still there to be resolved when sparkle itself moves.
  hook_notify "{\"hook_event_name\":\"PostToolUse\",\"cwd\":\"$a\"}"
  grep -q -- 'trill resolve holt/alpha/sparkle' "$FAKE_TRILL_LOG"
}

# A daemon that never took the ask leaves nothing armed: the next tool call must
# not pay for a resolve of a fin that was never on screen.
@test "notify: an ask trill refused arms no resolve" {
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" sparkle)"
  mktrill; export FAKE_TRILL_EXIT=2
  hook_notify "{\"hook_event_name\":\"Notification\",\"cwd\":\"$dir\"}"
  export FAKE_TRILL_EXIT=0
  : >"$FAKE_TRILL_LOG"
  run hook_notify "{\"hook_event_name\":\"PostToolUse\",\"cwd\":\"$dir\"}"
  [ "$status" -eq 0 ]
  [ ! -s "$FAKE_TRILL_LOG" ]
}

# ── reap ─────────────────────────────────────────────────────────────────────

@test "reap: removes a clean, landed, unoccupied checkout and its branch" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" sweepme)"
  git -C "$main" merge -q --no-edit worktree-sweepme
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"reaped sweepme (alpha)"* ]]
  [ ! -e "$dir" ]
  [ "$(reg_rows)" -eq 0 ]
}

# ── the ask markers a reap is the only answer to ─────────────────────────────
# `holt hook notify` keeps its resolve path cheap with one marker file per fin
# it put up, and clears that marker when the SESSION moves. Two shapes never
# move again: a lane blocked on you when its pane closed (it is answered by
# being reaped) and a pane outside every lane, keyed by session id. Left alone
# they accumulate one per abandoned question, the dir is never empty again, and
# the gate answers "yes, something is waiting" on every tool call in every pane
# for the life of the machine.

@test "reap: a lane reaped while it was blocked takes its fin down with it" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" sweepme)"
  git -C "$main" merge -q --no-edit worktree-sweepme
  mktrill
  local asks="$XDG_STATE_HOME/holt/asks"
  mkdir -p "$asks"; : >"$asks/holt.alpha.sweepme"

  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"reaped sweepme (alpha)"* ]]
  # The marker is gone, and so is the fin: its `Go to lane` action would run
  # `holt focus` against a lane that no longer exists.
  [ ! -e "$asks/holt.alpha.sweepme" ]
  grep -q -- 'resolve holt/alpha/sweepme' "$FAKE_TRILL_LOG"
  # And never the directory itself — something else on the machine watches it.
  [ -d "$asks" ]
}

@test "reap: an ordinary reap launches no trill at all" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" sweepme >/dev/null
  git -C "$main" merge -q --no-edit worktree-sweepme
  mktrill
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  # The marker is the gate on the launch, not an afterthought to it: a lane
  # that ended its turn cleanly has nothing on the ledge, and a sweep of forty
  # of them must not start forty processes.
  [ ! -s "$FAKE_TRILL_LOG" ]
}

@test "list: a marker older than a day is dropped, whatever it named" {
  local asks="$XDG_STATE_HOME/holt/asks"
  mkdir -p "$asks"
  : >"$asks/holt.session.7f3c"        # a pane outside every lane; its session ended
  : >"$asks/holt.alpha.fresh"
  touch -t 202001010000 "$asks/holt.session.7f3c"

  cd "$TMP"; wt_run                   # the listing sweeps
  [ "$status" -eq 0 ]
  [ ! -e "$asks/holt.session.7f3c" ]
  # Today's marker is exactly what the gate is for.
  [ -e "$asks/holt.alpha.fresh" ]
}

@test "reap: keeps a landed checkout that a pane is still standing in" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" busy)"
  git -C "$main" merge -q --no-edit worktree-busy
  export FAKE_LSOF_CWDS="$dir"
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"kept busy (alpha) — something is standing in the checkout"* ]]
  [ -e "$dir/.git" ]
}

@test "reap: an occupied lane NAMES the process, because lsof sees a cwd not a pane" {
  # The bug this exists for: a lane was kept with "a pane is open in it" and
  # there was no window anywhere on the machine. lsof does not observe panes,
  # it observes cwds — a dev server, a watcher, a telemetry daemon orphaned to
  # pid 1 days ago all pin a lane exactly as hard as a live agent, and read
  # identically once the pid is discarded. The verdict is unchanged (occupied
  # ⇒ keep); what has to change is that the evidence survives the sweep.
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" haunted)"
  git -C "$main" merge -q --no-edit worktree-haunted
  export FAKE_LSOF_CWDS="$dir/node_modules/next" FAKE_LSOF_CMD=node
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"pid 4001 node"* ]] || fail "the refusal named no process: $output"
  # A cwd DEEPER than the checkout says which subdirectory, which is usually
  # the whole diagnosis.
  [[ "$output" == *"in node_modules/next"* ]] || fail "$output"
  [[ "$output" == *"$dir"* ]] || fail "the refusal named no checkout to go and look at: $output"
  [ -e "$dir/.git" ]
}

@test "reap: an occupied lane exposes its holders in --json" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" watched)"
  export FAKE_LSOF_CWDS="$dir" FAKE_LSOF_CMD=node
  cd "$TMP"; wt_run list --json
  [ "$status" -eq 0 ]
  [[ "$output" == *'"occupied": true'* ]] || fail "$output"
  [[ "$output" == *'"pid": 4001'* ]] || fail "$output"
  [[ "$output" == *'"command": "node"'* ]] || fail "$output"
  [[ "$output" == *'"via": "lsof"'* ]] || fail "$output"
}

@test "reap: an unoccupied lane carries no occupied_by key at all" {
  # occupied_by is an ADDITION to a frozen envelope. Omitted when empty, so a
  # consumer that never learns the key sees exactly what it saw before.
  local main; main="$(mkrepo alpha)"; mkwt "$main" quiet >/dev/null
  cd "$TMP"; wt_run list --json
  [ "$status" -eq 0 ]
  [[ "$output" == *'"occupied": false'* ]] || fail "$output"
  [[ "$output" != *"occupied_by"* ]] || fail "an empty holder list must not appear: $output"
}

@test "list --json: a lane that never committed reads as fresh, not merged" {
  # The bug: a lane cut from main seconds ago is trivially an ancestor of main,
  # so the ancestry rung called it landed and every consumer that renders a
  # verdict — the rice's paw pill among them — labelled a brand-new lane
  # `merged`. "Nothing has happened here yet" is its own state.
  local main; main="$(mkrepo alpha)"; hook_create "$main" brandnew >/dev/null
  cd "$TMP"; wt_run list --json
  [ "$status" -eq 0 ]
  [[ "$output" == *'"verdict": "fresh"'* ]] || fail "$output"
  [[ "$output" == *'"via": "never-diverged"'* ]] || fail "$output"
}

@test "list --json: a branch whose commits really landed still reads as yes" {
  # The other half of the same rule: `fresh` must never swallow a real landing.
  # This one merged by fast-forward, so it has no commits of its own left to
  # count either — only its reflog remembers that it ever did anything.
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" done)"
  git -C "$main" merge -q --ff-only worktree-done
  cd "$TMP"; wt_run list --json
  [ "$status" -eq 0 ]
  [[ "$output" == *'"verdict": "yes"'* ]] || fail "$output"
  [[ "$output" == *'"via": "ancestry"'* ]] || fail "$output"
}

@test "list --json: work that arrived by cherry-pick is not 'fresh' either" {
  # The trap this test exists for: `commit:` is NOT every way git spells "this
  # branch did something". cherry-pick, revert, rebase and reset each write
  # their own reflog subject, so a rule that hunts for `commit…` calls all of
  # them fresh — this bug pointing the other way, a landed lane losing its
  # verdict. The rule is inverted instead: nothing but `branch: Created from`.
  local main dir; main="$(mkrepo alpha)"
  git -C "$main" checkout -q -b source
  commit_in "$main" side.txt "a commit made somewhere else"
  git -C "$main" checkout -q main
  dir="$(hook_create "$main" picked)"
  git -C "$dir" -c commit.gpgsign=false cherry-pick "$(git -C "$main" rev-parse source)" >/dev/null
  git -C "$main" merge -q --ff-only worktree-picked   # …and that is how it landed
  cd "$TMP"; wt_run list --json
  [ "$status" -eq 0 ]
  [[ "$output" != *'"verdict": "fresh"'* ]] || fail "a cherry-picked lane read as fresh: $output"
}

@test "reap: a fresh lane is still reapable — the new verdict is a label only" {
  # `fresh` splits what a reader is TOLD, never what the sweep does: there is
  # nothing on a never-committed branch to lose, exactly as before.
  local main dir; main="$(mkrepo alpha)"; dir="$(hook_create "$main" nothing)"
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"reaped nothing (alpha)"* ]] || fail "$output"
  [ ! -e "$dir" ]
  [ "$(reg_rows)" -eq 0 ]
}

@test "reap: keeps a landed checkout with uncommitted changes" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" messy)"
  git -C "$main" merge -q --no-edit worktree-messy
  echo edit >"$dir/README.md"
  cd "$TMP"; wt_run reap
  [ -e "$dir/.git" ]
  git -C "$main" show-ref -q --verify refs/heads/worktree-messy
}

@test "reap: a dirty lane NAMES what is in the way instead of going quiet" {
  # The lane that prompted this was landed, unoccupied, and held back by a
  # single untracked directory a tool had dropped in it. Reap said nothing
  # about it at all and closed with the generic three-reason line, so the
  # lane read as one holt had simply forgotten about.
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" fossil)"
  git -C "$main" merge -q --no-edit worktree-fossil
  mkdir -p "$dir/live"; echo junk >"$dir/live/reaped.log"
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"kept fossil (alpha)"* ]] || fail "the dirty lane went unnamed: $output"
  [[ "$output" == *"live/"* ]] || fail "the note didn't name the path in the way: $output"
  [[ "$output" == *"$dir"* ]] || fail "the note didn't say where to go look: $output"
  # And the generic line must NOT also fire — it reads as a second verdict.
  [[ "$output" != *"nothing to reap"* ]] || fail "the abstract line contradicted the concrete one: $output"
  [ -e "$dir/.git" ]
}

@test "reap: a dirty lane's note caps the paths it lists" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" littered)"
  git -C "$main" merge -q --no-edit worktree-littered
  local i; for i in 1 2 3 4 5; do echo x >"$dir/stray$i.txt"; done
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"+2 more"* ]] || fail "an uncapped note would be a screenful: $output"
}

@test "reap: keeps an unmerged checkout" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" unmerged)"
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"nothing to reap"* ]]
  [ -e "$dir/.git" ]
}

@test "reap: never removes the checkout it is being run from" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" self)"
  git -C "$main" merge -q --no-edit worktree-self
  cd "$dir"; wt_run reap
  [ -e "$dir/.git" ]
  git -C "$main" show-ref -q --verify refs/heads/worktree-self
}

@test "reap: without a usable lsof it degrades to parked-only and says so" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" cautious)"
  git -C "$main" merge -q --no-edit worktree-cautious
  export FAKE_LSOF_BROKEN=1
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"no lsof"* ]]
  [ -e "$dir/.git" ]     # a live checkout is never guessed at
}

@test "reap: a squash-merged branch is recognized via its merged PR" {
  local main dir tip; main="$(mkrepo alpha)"; dir="$(mkwt "$main" squashed)"
  tip="$(git -C "$dir" rev-parse HEAD)"
  git -C "$main" merge -q --squash worktree-squashed && git -C "$main" commit -qm "squash merge"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$tip"
  cd "$TMP"; wt_run reap
  [[ "$output" == *"reaped squashed (alpha)"* ]]
}

@test "reap: a merged PR whose SHA no longer matches the tip is left alone" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ahead)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)"
  commit_in "$dir" post.txt "un-landed work"
  cd "$TMP"; wt_run reap
  [ -e "$dir/.git" ]
  git -C "$main" show-ref -q --verify refs/heads/worktree-ahead
}

# ── the branch that outran its merged PR ─────────────────────────────────────
# The sweep has always KEPT these (the test above), which is right — and said
# nothing about them, which is how they went unnoticed: the PR reads merged
# everywhere you look, so a worktree sitting on un-shipped commits is
# indistinguishable from one still in flight. These three pin the naming.

@test "reap: names the branch whose PR merged but whose tip moved on" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" outran)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-outran
  commit_in "$dir" post.txt "work done after the PR merged"
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"kept outran (alpha) — merged PR #12, 1 commit(s) since"* ]] \
    || fail "reap kept the branch but never said why: $output"
  [[ "$output" == *"holt reship outran"* ]] || fail "reap named no way out of it"
}

# A second checkout of the same branch name pushed a merge before this one ever
# pulled: the local tip is real work, just not work built on top of what
# actually merged. Same nonzero commit count as "outran", opposite remedy —
# reshipping THIS tip would push content the merge already superseded.
@test "reap: names a branch whose merged PR the tip never built on, not 'outran'" {
  local main dir stale; main="$(mkrepo alpha)"; dir="$(mkwt "$main" diverged)"
  commit_in "$main" elsewhere.txt "landed via a different checkout entirely"
  stale="$(git -C "$main" rev-parse HEAD)"   # not reachable from worktree-diverged
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$stale" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-diverged
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"kept diverged (alpha) — merged PR #12, but the tip isn't built on what merged"* ]] \
    || fail "reap kept the branch but blamed the wrong cause: $output"
  [[ "$output" != *"holt reship diverged"* ]] \
    || fail "reap pointed a diverged (stale) branch at reship, which would push it: $output"
  git -C "$main" show-ref -q --verify refs/heads/worktree-diverged \
    || fail "the branch was deleted despite Landed() correctly saying no"
}

# A squash merge puts the branch's content on main under a NEW sha, and the
# lane then rebases onto main and keeps working — the single most ordinary
# shape there is. Ancestry against the PR's pre-squash tip says "sideways", and
# the remedy for sideways is DELETE THE CHECKOUT, so this misread cost real
# commits. What settles it is that main is an ancestor of the tip.
@test "reap: a lane rebased past its squash-merged PR is 'outran', not diverged" {
  local main dir squashed; main="$(mkrepo alpha)"; dir="$(mkwt "$main" rebased)"
  squashed="$(git -C "$dir" rev-parse HEAD)"   # what the PR was opened at
  # main squash-merges it: same content, a sha the branch has never seen.
  git -C "$main" merge -q --squash worktree-rebased
  git -C "$main" commit -qm "squash merge of #12"
  # the lane rebases onto main, dropping its own copies, then keeps working
  git -C "$dir" rebase -q main
  commit_in "$dir" post.txt "work done after the PR merged"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$squashed" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-rebased
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"kept rebased (alpha) — merged PR #12, 1 commit(s) since"* ]] \
    || fail "a rebased-past-a-squash lane was not read as new work: $output"
  [[ "$output" != *"isn't built on what merged"* ]] \
    || fail "reap told a correctly rebased lane to delete itself: $output"
  [ -e "$dir/.git" ]
}

# The +N marker promises "commits no PR covers". It read merged PRs only, so
# the follow-up PR `holt reship` had just opened was invisible to it and the
# lane went on demanding a reship that had already happened.
@test "list: an open PR at the tip clears the +N marker" {
  local main dir tip; main="$(mkrepo alpha)"; dir="$(mkwt "$main" shipped)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-shipped
  commit_in "$dir" post.txt "work done after the PR merged"
  tip="$(git -C "$dir" rev-parse HEAD)"
  cd "$TMP"; wt_run
  [[ "$output" == *"live+1"* ]] || fail "the un-shipped commit was not marked: $output"
  # …now reship it: an open PR stands at exactly that tip.
  export FAKE_GH_OPEN_BRANCH=worktree-shipped FAKE_GH_OPEN_OID="$tip" FAKE_GH_OPEN_PR=13
  rm -rf "$CLAUDE_WT_BASE/.cache"
  cd "$TMP"; wt_run
  [[ "$output" != *"live+1"* ]] \
    || fail "the marker still demanded a reship the open PR already covers: $output"
}

# …but only at the tip. A commit made after the push is genuinely uncovered,
# and that is the whole case the marker exists for.
@test "list: a commit made after the open PR's tip is still marked +N" {
  local main dir tip; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ahead-of-pr)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-ahead-of-pr
  commit_in "$dir" post.txt "pushed, and the PR covers it"
  tip="$(git -C "$dir" rev-parse HEAD)"
  commit_in "$dir" post2.txt "committed after the push — nothing covers this"
  export FAKE_GH_OPEN_BRANCH=worktree-ahead-of-pr FAKE_GH_OPEN_OID="$tip" FAKE_GH_OPEN_PR=13
  cd "$TMP"; wt_run
  [[ "$output" == *"live+2"* ]] \
    || fail "an open PR behind the tip silenced the marker anyway: $output"
}

@test "list: a branch that outran its merged PR is marked +N" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" outran)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-outran
  commit_in "$dir" post.txt "one"
  commit_in "$dir" post2.txt "two"
  cd "$TMP"; wt_run
  [ "$status" -eq 0 ]
  [[ "$output" == *"live+2"* ]] || fail "the state column hid the un-shipped commits: $output"
  [[ "$output" == *"holt reship"* ]] || fail "the +N marker was printed with no legend"
}

@test "list: a branch diverged from its merged PR is marked ~N, not +N" {
  local main dir stale; main="$(mkrepo alpha)"; dir="$(mkwt "$main" diverged)"
  commit_in "$main" elsewhere.txt "landed via a different checkout entirely"
  stale="$(git -C "$main" rev-parse HEAD)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$stale" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-diverged
  cd "$TMP"; wt_run
  [ "$status" -eq 0 ]
  [[ "$output" == *"live~1"* ]] || fail "a diverged tip was marked as though it outran its PR: $output"
  [[ "$output" != *"live+1"* ]] || fail "diverged and outran share a marker: $output"
  [[ "$output" == *"remove the checkout instead of reshipping"* ]] \
    || fail "the ~N marker was printed with no legend, or the wrong one: $output"
}

@test "list: a branch whose post-merge commits ALSO landed is not marked" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" landed)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-landed
  commit_in "$dir" post.txt "more"
  git -C "$main" merge -q --no-edit worktree-landed     # …and that landed too
  cd "$TMP"; wt_run
  [[ "$output" != *"+1"* ]] || fail "a branch fully in main was flagged as un-shipped: $output"
}

@test "list: +N counts THIS lane's un-shipped commits, not main's that it caught up on" {
  # The marker promises "commits no PR covers". A long-lived lane keeps catching
  # up on the default branch, and every commit that ride brings along is
  # reachable from the tip but NOT from the merged head — so a bare
  # `head..branch` bills other people's already-landed work to this lane. A real
  # one read `live+131` for two commits of its own, which reads as "unreviewable,
  # deal with it later" instead of "one PR, two commits".
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" longlived)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-longlived
  commit_in "$main" one.txt "someone else's work"      # three other PRs land
  commit_in "$main" two.txt "and more"
  commit_in "$main" three.txt "and more still"
  git -C "$dir" rebase -q main                          # the lane catches up
  commit_in "$dir" mine.txt "the commit that really is un-shipped"
  cd "$TMP"; wt_run
  [[ "$output" == *"live+2"* ]] \
    || fail "the marker billed main's landed commits to this lane: $output"
}

@test "list: the +N count agrees with the commit list reship would put in the PR" {
  # `reshipBody` already lists `base..branch`, so before this agreed, `reship`
  # announced "131 commit(s) past the merge" and opened a PR whose body listed
  # two. Same lane, same moment, two numbers — the marker is what has to move.
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" agrees)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-agrees
  commit_in "$main" one.txt "someone else's work"
  commit_in "$main" two.txt "and more"
  git -C "$dir" rebase -q main
  commit_in "$dir" mine.txt "un-shipped"
  local listed; listed="$(git -C "$main" rev-list --count main..worktree-agrees)"
  cd "$TMP"; wt_run
  [[ "$output" == *"live+$listed"* ]] \
    || fail "the marker says something other than the $listed commit(s) reship would list: $output"
}

@test "list: an ordinary in-flight branch keeps a bare state column" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" plain >/dev/null
  cd "$TMP"; wt_run
  [[ "$output" == *"live"* ]]
  [[ "$output" != *"live+"* ]] || fail "a branch with no merged PR was marked as outrunning one"
  [[ "$output" != *"reship"* ]] || fail "the legend printed on a listing that earned no marker"
}

@test "reap: 'landed' means landed on the DEFAULT branch, not whatever main has checked out" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" sidequest)"
  git -C "$main" checkout -qb detour
  git -C "$main" merge -q --no-edit worktree-sidequest   # landed on `detour`, NOT on main
  cd "$TMP"; wt_run reap
  git -C "$main" show-ref -q --verify refs/heads/worktree-sidequest
}

# ── dead ends: PR closed, repo archived ──────────────────────────────────────

@test "reap: a lane whose PR was CLOSED unmerged is named, not swept" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" rejected >/dev/null
  export FAKE_GH_CLOSED_PR=43
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"PR #43 was closed unmerged"* ]] \
    || fail "a lane nothing will ever land said nothing about it: $output"
  [[ "$output" == *"holt drop rejected"* ]] || fail "reap named no way out of it: $output"
  # The commits were REJECTED, not landed. An automatic sweep must never take them.
  git -C "$main" show-ref -q --verify refs/heads/worktree-rejected \
    || fail "reap deleted unlanded work because a PR was closed"
}

@test "reap: a lane in an ARCHIVED repo is named, not swept" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" frozen >/dev/null
  export FAKE_GH_ARCHIVED=true
  cd "$TMP"; wt_run reap
  [[ "$output" == *"archived on the forge"* ]] \
    || fail "an unlandable lane in an archived repo said nothing: $output"
  git -C "$main" show-ref -q --verify refs/heads/worktree-frozen \
    || fail "reap deleted work that can no longer be submitted anywhere"
}

@test "reap: a merged PR still reads as reship, never as a dead end" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" both)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-both FAKE_GH_CLOSED_PR=43
  commit_in "$dir" post.txt "after the merge"
  cd "$TMP"; wt_run reap
  [[ "$output" == *"holt reship both"* ]] || fail "the merged PR lost to the closed one: $output"
  [[ "$output" != *"closed unmerged"* ]] || fail "a branch that DID land was called a dead end: $output"
}

# ── drop + the reap ledger ───────────────────────────────────────────────────

@test "drop: takes an unlanded lane reap won't, and prints the undo" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" doomed >/dev/null
  local sha; sha="$(git -C "$main" rev-parse worktree-doomed)"
  cd "$TMP"; wt_run drop doomed
  [ "$status" -eq 0 ]
  git -C "$main" show-ref -q --verify refs/heads/worktree-doomed \
    && fail "drop left the branch behind"
  [[ "$output" == *"${sha:0:12}"* ]] || fail "drop deleted a branch without printing the SHA back: $output"
  [[ "$output" == *"undo:"* ]] || fail "drop named no way back"
  [ "$(reg_rows)" -eq 0 ] || fail "drop left the registry row behind"
}

@test "drop: refuses a dirty checkout — an unlanded lane's dirt has no PR to fall back on" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" messy)"
  echo scratch >"$dir/uncommitted.txt"
  cd "$TMP"; wt_run drop messy
  [ "$status" -eq 2 ] || fail "expected a refusal (exit 2), got $status: $output"
  git -C "$main" show-ref -q --verify refs/heads/worktree-messy \
    || fail "a refused drop still deleted the branch"
  [ -f "$dir/uncommitted.txt" ] || fail "a refused drop still ate the working tree"
}

@test "drop: refuses a lane a pane is standing in, and names what is standing there" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" busy)"
  export FAKE_LSOF_CWDS="$dir" FAKE_LSOF_CMD=node
  cd "$TMP"; wt_run drop busy
  [ "$status" -eq 2 ] || fail "expected a refusal (exit 2), got $status: $output"
  # "close it first" is useless advice when the occupant is a dev server rather
  # than a window. A drop is a refusal a human must clear, so it has to say what.
  [[ "$output" == *"pid 4001 node"* ]] || fail "the refusal named no process: $output"
  git -C "$main" show-ref -q --verify refs/heads/worktree-busy || fail "drop yanked an occupied lane"
}

@test "drop: an unknown name dies pointing at the listing" {
  mkrepo alpha >/dev/null
  cd "$TMP"; wt_run drop nosuchlane
  [ "$status" -eq 1 ]
  [[ "$output" == *"holt"* ]] || fail "the refusal named no way to find the real names"
}

@test "reaped: every reap leaves a line saying what died and how to get it back" {
  # The bug this exists for: a lane vanished mid-session and NOTHING could say
  # why — `git branch -D` takes the branch's reflog with it, `git worktree
  # remove` takes the admin dir, and holt kept no record of its own most
  # destructive act.
  local main; main="$(mkrepo alpha)"; mkwt "$main" gone >/dev/null
  local sha; sha="$(git -C "$main" rev-parse worktree-gone)"
  git -C "$main" merge -q --no-edit worktree-gone     # landed by ancestry
  cd "$TMP"; "$WT" reap >/dev/null 2>&1
  wt_run reaped
  [ "$status" -eq 0 ]
  [[ "$output" == *"alpha/gone"* ]] || fail "the ledger doesn't name the lane: $output"
  [[ "$output" == *"ancestry"* ]] || fail "the ledger doesn't say which rung justified it: $output"
  [[ "$output" == *"${sha:0:12}"* ]] || fail "the ledger kept no SHA, so nothing is recoverable: $output"
  [[ "$output" == *"branch worktree-gone"* ]] || fail "the ledger names no recovery command: $output"
}

@test "reaped: the recorded SHA really does restore the branch" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" undoable >/dev/null
  local sha; sha="$(git -C "$main" rev-parse worktree-undoable)"
  cd "$TMP"; wt_run drop undoable
  git -C "$main" show-ref -q --verify refs/heads/worktree-undoable && fail "drop didn't delete"
  # Read the SHA back out of the ledger, not out of the test's own variable —
  # that is the path a human actually walks.
  local recorded; recorded="$(awk -F'\t' '$3=="undoable"{print $5}' "$XDG_STATE_HOME/scruff/reaped.log")"
  [ "$recorded" = "$sha" ] || fail "ledger SHA $recorded != $sha"
  git -C "$main" branch worktree-undoable "$recorded"
  [ "$(git -C "$main" rev-parse worktree-undoable)" = "$sha" ] || fail "the branch came back wrong"
}

@test "reaped: an empty ledger says so and names its own path" {
  mkrepo alpha >/dev/null
  cd "$TMP"; wt_run reaped
  [ "$status" -eq 0 ]
  [[ "$output" == *"no lanes reaped"* ]] || fail "an empty ledger printed something else: $output"
  [[ "$output" == *"reaped.log"* ]] || fail "an empty ledger didn't say where it would live"
}

@test "reap: is idempotent — a second run finds nothing and changes nothing" {
  local main; main="$(mkrepo alpha)"
  mkwt "$main" twice >/dev/null
  git -C "$main" merge -q --no-edit worktree-twice
  cd "$TMP"; "$WT" reap >/dev/null 2>&1
  wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"nothing to reap"* ]]
}

# ── reship ───────────────────────────────────────────────────────────────────
# The way OUT of the state above. A real remote is needed here (the other tests
# only ever parse origin's URL), so these point origin at a bare repo on disk.

no_pr_created() { # 0 when gh was never asked to open a PR (the log may not exist at all)
  ! grep -q "pr create" "$FAKE_GH_LOG" 2>/dev/null
}

mkremote() { # mkremote <main> — give a repo a bare origin it can actually push to
  local bare="$TMP/remotes/$(basename "$1").git"
  mkdir -p "$(dirname "$bare")"
  git init -q --bare -b main "$bare"
  git -C "$1" remote set-url origin "$bare"
  printf '%s' "$bare"
}

@test "reship: pushes the branch and opens the follow-up PR" {
  local main dir bare; main="$(mkrepo alpha)"; dir="$(mkwt "$main" outran)"
  bare="$(mkremote "$main")"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-outran
  commit_in "$dir" post.txt "work done after the PR merged"
  cd "$TMP"; wt_run reship outran
  [ "$status" -eq 0 ]
  git -C "$bare" show-ref -q --verify refs/heads/worktree-outran \
    || fail "the branch was never pushed, so the follow-up PR would be empty"
  grep -q "pr create" "$FAKE_GH_LOG" || fail "no PR was opened: $output"
  [[ "$output" == *"follow-up PR open"* ]]
}

@test "reship: refuses a diverged tip instead of pushing stale content" {
  local main dir bare stale; main="$(mkrepo alpha)"; dir="$(mkwt "$main" diverged)"
  bare="$(mkremote "$main")"
  commit_in "$main" elsewhere.txt "landed via a different checkout entirely"
  stale="$(git -C "$main" rev-parse HEAD)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$stale" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-diverged
  cd "$TMP"; wt_run reship diverged
  [ "$status" -ne 0 ]
  [[ "$output" == *"does not build on that merged PR"* ]] \
    || fail "reship refused for the wrong reason, or didn't refuse: $output"
  git -C "$bare" show-ref -q --verify refs/heads/worktree-diverged \
    && fail "reship pushed a diverged tip despite refusing"
  no_pr_created || fail "a PR was opened for content the merge already superseded"
}

@test "reship: an already-open PR takes the push and no second PR" {
  local main dir bare; main="$(mkrepo alpha)"; dir="$(mkwt "$main" inflight)"
  bare="$(mkremote "$main")"
  export FAKE_GH_OPEN_URL="https://github.com/acme/alpha/pull/3"
  cd "$TMP"; wt_run reship inflight
  [ "$status" -eq 0 ]
  git -C "$bare" show-ref -q --verify refs/heads/worktree-inflight
  no_pr_created || fail "a second PR was opened over an open one"
  [[ "$output" == *"already covers this branch"* ]]
}

@test "reship: a branch with nothing past main refuses rather than opening an empty PR" {
  local main; main="$(mkrepo alpha)"
  hook_create "$main" empty >/dev/null      # a worktree, no commits of its own
  mkremote "$main" >/dev/null
  cd "$TMP"; wt_run reship empty
  [ "$status" -ne 0 ]
  [[ "$output" == *"nothing the main branch doesn't already have"* ]]
  no_pr_created || fail "an empty PR was opened"
}

@test "reship: an unknown name dies pointing at the listing" {
  mkrepo alpha >/dev/null
  cd "$TMP"; wt_run reship nosuch
  [ "$status" -ne 0 ]
  [[ "$output" == *"no lane named 'nosuch'"* ]]
}

# ── child ────────────────────────────────────────────────────────────────────

@test "child: worktrees another repo and registers THIS pane as the parent" {
  local a b dir; a="$(mkrepo alpha)"; b="$(mkrepo beta)"
  cd "$a"
  run bash -c "cd '$a' && '$WT' child '$b' cross 2>/dev/null"
  [ "$status" -eq 0 ]
  dir="$output"
  [ "$dir" = "$CLAUDE_WT_BASE/beta/cross" ]
  [ "$(git -C "$dir" branch --show-current)" = worktree-cross ]
  # 5th registry field is the spawning cwd — this is what the statusline reads.
  [ "$(awk -F'\t' -v p="$dir" '$4==p{print $5}' "$REG")" = "$a" ]
}

@test "child: defaults the name to this pane's own worktree name" {
  local a b dir; a="$(mkrepo alpha)"; b="$(mkrepo beta)"
  dir="$(mkwt "$a" shared)"
  run bash -c "cd '$dir' && '$WT' child '$b' 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$output" = "$CLAUDE_WT_BASE/beta/shared" ]
}

@test "child: refuses a name whose branch or path already exists" {
  local a b; a="$(mkrepo alpha)"; b="$(mkrepo beta)"
  "$WT" child "$b" taken >/dev/null 2>&1
  run bash -c "cd '$a' && '$WT' child '$b' taken"
  [ "$status" -ne 0 ]
  [[ "$output" == *"already exists"* ]]
}

@test "child: refuses a path that isn't a repo, and a linked worktree" {
  run "$WT" child "$TMP/nope"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no such directory"* ]]
  mkdir -p "$TMP/plain"
  run "$WT" child "$TMP/plain"
  [ "$status" -ne 0 ]
  [[ "$output" == *"isn't inside a git repo"* ]]
}

@test "child: a resumed child inherits its parent's chat, not an empty picker" {
  local a b dir cdir; a="$(mkrepo alpha)"; b="$(mkrepo beta)"
  dir="$(mkwt "$a" par)"
  mkdir -p "$HOME/.claude/projects/$(printf '%s' "$dir" | sed 's/[/.]/-/g')"
  cdir="$(cd "$dir" && "$WT" child "$b" 2>/dev/null)"
  commit_in "$cdir" c.txt "child work"
  git -C "$b" worktree remove --force "$cdir"
  wt_run resume beta/par
  [ "$status" -eq 0 ]
  [[ "$output" == *"spawned from a pane in $dir"* ]]
  # The parent is a SHARED checkout with many conversations in it, so "the
  # newest one" isn't an answer holt is entitled to give — this is the one case
  # the picker is right.
  [[ "$output" == *"claude --resume"* ]] || fail "a shared parent needs the picker: $output"
}

# ── spawn ────────────────────────────────────────────────────────────────────

@test "spawn: names the worktree and parents it to the repo, not to a pane" {
  local b dir; b="$(mkrepo beta)"
  # No cd: the palette runs under launchd, from wherever it happens to be.
  run bash -c "'$WT' spawn '$b' fix-the-notch 2>/dev/null"
  [ "$status" -eq 0 ]
  dir="$output"
  [ "$dir" = "$CLAUDE_WT_BASE/beta/fix-the-notch" ]
  [ "$(git -C "$dir" branch --show-current)" = worktree-fix-the-notch ]
  # Parent is the repo's own main checkout — a pane sitting there lists it.
  [ "$(awk -F'\t' -v p="$dir" '$4==p{print $5}' "$REG")" = "$b" ]
}

@test "spawn: a taken name takes the next free suffix instead of dying" {
  local b first second; b="$(mkrepo beta)"
  first="$("$WT" spawn "$b" dupe 2>/dev/null)"
  run bash -c "'$WT' spawn '$b' dupe 2>/dev/null"
  [ "$status" -eq 0 ]
  second="$output"
  [ "$first" = "$CLAUDE_WT_BASE/beta/dupe" ]
  [ "$second" = "$CLAUDE_WT_BASE/beta/dupe-2" ]
  [ "$(git -C "$second" branch --show-current)" = worktree-dupe-2 ]
}

@test "spawn: a free path with a taken BRANCH still skips to a free name" {
  local b; b="$(mkrepo beta)"
  git -C "$b" branch worktree-held
  run bash -c "'$WT' spawn '$b' held 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$output" = "$CLAUDE_WT_BASE/beta/held-2" ]
}

@test "spawn: records its client, independently of the future default" {
  local b dir; b="$(mkrepo beta)"
  dir="$("$WT" spawn "$b" codex-task codex 2>/dev/null)"
  [ "$(awk -F'\t' -v p="$dir" '$4==p{print $6}' "$REG")" = codex ]
  HAUS_AGENT_DEFAULT=opencode run "$WT" resume codex-task
  [ "$status" -eq 0 ]
  [[ "$output" == *"codex resume"* ]]
  [[ "$output" != *"opencode --continue"* ]]
}

# The fourth client, end to end: the row records it, and `resume` picks pi's
# continue-the-newest rung rather than the machine default's.
@test "new: a pi lane records pi, and reopens with pi --continue" {
  local b dir; b="$(mkrepo beta)"
  shim_agent pi
  cd "$b"
  run "$WT" new pi-task pi
  [ "$status" -eq 0 ]
  dir="$CLAUDE_WT_BASE/beta/pi-task"
  [ "$(awk -F'\t' -v p="$dir" '$4==p{print $6}' "$REG")" = pi ]
  [[ "$output" == *"ran pi"* ]]

  HAUS_AGENT_DEFAULT=claude run "$WT" resume pi-task
  [ "$status" -eq 0 ]
  [[ "$output" == *"pi --continue"* ]]
  [[ "$output" != *"claude --continue"* ]]
}

@test "resume: pre-client registry rows remain Claude worktrees" {
  local main dir; main="$(mkrepo alpha)"; dir="$CLAUDE_WT_BASE/alpha/legacy"
  git -C "$main" branch worktree-legacy
  mkdir -p "$(dirname "$REG")"
  printf 'legacy\t%s\tworktree-legacy\t%s\t%s\n' "$main" "$dir" "$main" >"$REG"
  HAUS_AGENT_DEFAULT=codex run "$WT" resume legacy
  [ "$status" -eq 0 ]
  [[ "$output" == *"claude --continue"* ]]
}

@test "resume: a row naming a retired client reopens in Claude" {
  local main dir; main="$(mkrepo alpha)"; dir="$CLAUDE_WT_BASE/alpha/retired"
  git -C "$main" branch worktree-retired
  mkdir -p "$(dirname "$REG")"
  printf 'retired\t%s\tworktree-retired\t%s\t%s\tjcode\n' "$main" "$dir" "$main" >"$REG"
  HAUS_AGENT_DEFAULT=codex run "$WT" resume retired
  [ "$status" -eq 0 ]
  [[ "$output" == *"claude --continue"* ]]
}

@test "agent start: Codex receives a captured screenshot as an initial image" {
  cat >"$BIN/codex" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@"
EOF
  chmod +x "$BIN/codex"
  local image="$TMP/shot.png"; : >"$image"
  run "$WT" agent start codex --image "$image" -- "inspect this"
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '%s\n%s\n%s\n%s' -i "$image" -- "inspect this")" ]
}

# A prompt is DATA. Pounce's Spawn Agent box takes a paragraph, and a task typed
# there is very often a markdown list — so its first character is `-`, and a bare
# argv element starting with a dash is a FLAG to every one of these clients. This
# used to kill the pane on `error: unknown option '- …'` before the agent ran.
@test "agent start: a prompt starting with a dash reaches the client as text" {
  local prompt='- update the README
- and its footer'
  for client in claude codex opencode pi; do
    cat >"$BIN/$client" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@"
EOF
    chmod +x "$BIN/$client"
    run "$WT" agent start "$client" -- "$prompt"
    [ "$status" -eq 0 ]
    [[ "$output" == *"- update the README"* ]]
    [[ "$output" == *"- and its footer"* ]]
    # …and it arrived somewhere a parser can no longer read as an option.
    [[ "$output" == *"--"* ]]
  done
}

# ── new (the client-agnostic ⌘C) ─────────────────────────────────────────────
#
# `wt new` is what Super c runs when the default client isn't Claude Code — the
# only client that can make its own worktree. It must land the SAME checkout,
# branch and registry row the create hook does, parented to the pane's cwd, and
# then hand the pane over to the client. A shim client stands in for that exec.

shim_agent() { # shim_agent <name> — a client that prints how it was invoked
  cat >"$BIN/$1" <<EOF
#!/usr/bin/env bash
printf 'ran %s %s\n' "$1" "\$*"
EOF
  chmod +x "$BIN/$1"
}

@test "new: worktree of THIS repo, parented to the pane, then opens the client" {
  local b dir; b="$(mkrepo beta)"
  shim_agent opencode
  cd "$b"
  run "$WT" new notch-fix opencode
  [ "$status" -eq 0 ]
  dir="$CLAUDE_WT_BASE/beta/notch-fix"
  [ -e "$dir/.git" ]
  [ "$(git -C "$dir" branch --show-current)" = worktree-notch-fix ]
  # Parent is the PANE's cwd (as the create hook records it), not the repo, and
  # the row keeps the client so a later `wt notch-fix` reopens opencode.
  [ "$(awk -F'\t' -v p="$dir" '$4==p{print $5}' "$REG")" = "$b" ]
  [ "$(awk -F'\t' -v p="$dir" '$4==p{print $6}' "$REG")" = opencode ]
  [[ "$output" == *"ran opencode"* ]]
}

@test "new: by default it just makes the lane and prints the path" {
  local b dir; b="$(mkrepo beta)"
  shim_agent claude
  cd "$b"
  run "$WT" new quiet-one
  [ "$status" -eq 0 ]
  dir="$CLAUDE_WT_BASE/beta/quiet-one"
  [ -e "$dir/.git" ]
  # Only the path on stdout, so `cd "$(holt new)"` works — and NO client ran:
  # a lane is a checkout, and what you open in it is your business.
  [ "$(printf '%s\n' "$output" | tail -1)" = "$dir" ]
  [[ "$output" != *"ran claude"* ]]
}

@test "new: --open hands the pane to the client, --cmd to anything else" {
  local b; b="$(mkrepo beta)"
  shim_agent codex
  cd "$b"
  run "$WT" new opened --open codex
  [ "$status" -eq 0 ]
  [[ "$output" == *"ran codex"* ]]

  run "$WT" new scripted --cmd 'echo ran-cmd-in "$PWD"'
  [ "$status" -eq 0 ]
  [[ "$output" == *"ran-cmd-in"* ]]
  [[ "$output" == *"beta/scripted"* ]]

  # The two endings contradict each other; saying both is a typo, not a plan.
  run "$WT" new both --open --cmd 'true'
  [ "$status" -eq 1 ]
  [[ "$output" == *"pick one"* ]]
}

@test "new: an unnamed spawn names itself, and a taken name takes a suffix" {
  local b first second; b="$(mkrepo beta)"
  shim_agent claude
  cd "$b"
  run "$WT" new
  [ "$status" -eq 0 ]
  first="$(awk -F'\t' 'NR==1{print $1}' "$REG")"
  [ -n "$first" ]
  run "$WT" new "$first"          # ask for the taken one on purpose
  [ "$status" -eq 0 ]
  second="$(awk -F'\t' -v n="$first" '$1!=n{print $1}' "$REG")"
  [ "$second" = "$first-2" ]
}

@test "new: outside a repo it refuses rather than leaving a stray worktree" {
  shim_agent claude
  mkdir -p "$TMP/plain"
  cd "$TMP/plain"
  run "$WT" new
  [ "$status" -ne 0 ]
  [[ "$output" == *"not inside a git repo"* ]]
  [ "$(reg_rows)" -eq 0 ]
}

@test "new: an uninstalled client is named, and the checkout survives to resume" {
  local b; b="$(mkrepo beta)"
  cd "$b"
  # "codex is not installed" has to be SIMULATED, and simulating it takes both
  # halves below, because either one alone is defeated by the other mechanism:
  #
  #   HOLT_PATH_RESCUE=0  — holt appends a bare-PATH rescue for the hook case
  #                         (see internal/commands/path.go), and that rescue
  #                         re-adds the profile bindir a real codex lives in.
  #   PATH=$BIN:onlygit   — and the caller's PATH has to lose codex too, via a
  #                         directory holding nothing but a git symlink. Narrowing
  #                         to git's OWN directory is not enough: on a Nix box git
  #                         and codex share one profile bindir.
  #
  # Without both, this test passes in CI (no codex there) and silently fails on
  # any machine that has one — which is exactly what it did, in this suite and in
  # haus's copy, for as long as it existed.
  mkdir -p "$TMP/onlygit"
  ln -sf "$(command -v git)" "$TMP/onlygit/git"
  run env HOLT_PATH_RESCUE=0 PATH="$BIN:$TMP/onlygit" "$WT" new stranded codex
  [ "$status" -ne 0 ]
  [[ "$output" == *"codex is unavailable"* ]]
  [ -e "$CLAUDE_WT_BASE/beta/stranded/.git" ]
}

@test "spawn: refuses a missing path, a non-repo, and a missing name" {
  local b; b="$(mkrepo beta)"
  run "$WT" spawn "$TMP/nope" x
  [ "$status" -ne 0 ]
  [[ "$output" == *"no such directory"* ]]
  mkdir -p "$TMP/plain"
  run "$WT" spawn "$TMP/plain" x
  [ "$status" -ne 0 ]
  [[ "$output" == *"isn't inside a git repo"* ]]
  run "$WT" spawn "$b"
  [ "$status" -ne 0 ]
  [[ "$output" == *"usage: holt spawn"* ]]
}

# ── dangling checkouts (husks) ───────────────────────────────────────────────
#
# `git worktree remove` deletes the repo's admin dir (.git/worktrees/<id>) BEFORE
# it deletes the working tree. When that second half fails — an ignored build dir
# it cannot unlink, a file another process holds — what's left is a directory
# whose .git file points at a gitdir that no longer exists. `[ -e "$wt/.git" ]`
# says "live"; every git command run inside says "fatal: not a git repository".
#
# husk() reproduces exactly that end state, which is all any caller can observe.

husk() { # husk <main> <checkout> — leave <checkout> on disk, unregistered
  local id; id="$(basename "$2")"
  rm -rf "$1/.git/worktrees/$id"
}

@test "husk: a checkout git has disowned lists as 'stray', not 'live'" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ghosted)"
  husk "$main" "$dir"
  wt_run
  [ "$status" -eq 0 ]
  [[ "$output" == *ghosted* ]]
  # The whole point: the old `-e .git` test called this live, so the row lied and
  # `wt ghosted` refused to rebuild it — the branch was unreachable through wt.
  [[ "$output" != *"ghosted"*"live"* ]]
  [[ "$output" == *stray* ]]
}

@test "husk: the listing says what to do about it, and the sweep spares the branch" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ghosted)"
  # Landed AND merged-by-PR: every reason the sweep has to reap, so the only thing
  # keeping the branch alive is the husk rule itself.
  git -C "$main" merge -q --no-ff -m merge worktree-ghosted
  husk "$main" "$dir"
  FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$main" rev-parse worktree-ghosted)" wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *dangling* ]]
  git -C "$main" show-ref -q --verify refs/heads/worktree-ghosted \
    || fail "the branch was reaped while its checkout was a husk — the husk's uncommitted files are now referenced by nothing"
  [ -d "$dir" ] || fail "the husk directory was deleted"
}

@test "husk: resume moves it aside — never deletes it — and rebuilds the checkout" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ghosted)"
  # An edit that exists ONLY here: not committed, not on the branch. This is the
  # thing a husk can be holding, and the reason it is moved rather than removed.
  echo "only-copy" >"$dir/unsaved.txt"
  husk "$main" "$dir"
  wt_run resume ghosted
  [ "$status" -eq 0 ]
  # Rebuilt, and git can read it again.
  git -C "$dir" rev-parse --git-dir >/dev/null 2>&1 \
    || fail "resume left the husk in place instead of rebuilding the checkout"
  [ "$(git -C "$dir" branch --show-current)" = worktree-ghosted ]
  # And the old contents survive beside it, named in the output.
  local moved; moved="$(echo "$dir".stray-*)"
  [ -f "$moved/unsaved.txt" ] || fail "the husk's only copy of unsaved.txt is gone"
  [[ "$output" == *"$moved"* ]]
}

# A git whose `worktree remove` fails the way the real one does when the delete
# breaks down half-way: admin dir gone, working tree still standing, non-zero
# exit. Everything else passes straight through to the real git. Scoped to the
# one test that needs it — PATH is shim-first, and wt appends its rescue path
# precisely so a shim wins.
git_husk_shim() { # git_husk_shim — echoes a dir to put at the front of PATH
  local shim="$TMP/gitshim"
  mkdir -p "$shim"
  cat >"$shim/git" <<EOF
#!/usr/bin/env bash
if [ "\$3" = worktree ] && [ "\$4" = remove ]; then
  for a in "\$@"; do last="\$a"; done
  rm -rf "\$2/.git/worktrees/\$(basename "\$last")"
  exit 1
fi
exec $(command -v git) "\$@"
EOF
  chmod +x "$shim/git"
  printf '%s' "$shim"
}

@test "husk: the remove hook finishes the deletion git abandoned" {
  local main dir shim; main="$(mkrepo alpha)"; dir="$(mkwt "$main" messy)"
  echo "edit" >>"$dir/work.txt"          # dirty, so the wip-commit path runs too
  shim="$(git_husk_shim)"
  PATH="$shim:$PATH" hook_remove "$dir"
  # The uncommitted edit went to a wip commit as always, so nothing on disk was
  # irreplaceable — and only then is the hook allowed to finish what git started.
  [ ! -e "$dir" ] || fail "the hook left a husk at $dir; it would read 'live' forever and freeze the statusline"
  git -C "$main" show-ref -q --verify refs/heads/worktree-messy \
    || fail "the branch was dropped along with the residue"
  [[ "$(git -C "$main" log -1 --format=%s worktree-messy)" == wip:* ]] \
    || fail "the dirty tree wasn't parked before the checkout was deleted"
}

@test "husk: residue nothing can delete is reported, not died on" {
  [ "$(id -u)" != 0 ] || skip "root ignores the directory permissions this test uses"
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" stubborn)"
  # An IGNORED directory that cannot be unlinked (no write permission on its
  # parent) defeats git's delete — and ours. The contract is that the hook still
  # exits cleanly, keeps the branch, and names what it left behind.
  mkdir -p "$dir/scratch"
  echo build >"$dir/scratch/out.o"
  echo scratch/ >"$dir/.gitignore"
  git -C "$dir" add -A
  git -C "$dir" -c commit.gpgsign=false commit -qm ignore
  chmod 555 "$dir/scratch"
  run hook_remove "$dir"
  chmod 755 "$dir/scratch" 2>/dev/null || true
  [ "$status" -eq 0 ] || fail "the remove hook died on a checkout it couldn't delete"
  git -C "$main" show-ref -q --verify refs/heads/worktree-stubborn \
    || fail "the branch was dropped even though the checkout survived"
}

# ── registry upkeep ──────────────────────────────────────────────────────────

@test "registry: rows whose branch has vanished are pruned on the next listing" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ghost)"
  git -C "$main" worktree remove --force "$dir"
  git -C "$main" branch -qD worktree-ghost
  [ "$(reg_rows)" -eq 1 ]
  "$WT" list >/dev/null 2>&1
  [ "$(reg_rows)" -eq 0 ]
}

@test "registry: a row pointing at a deleted main checkout doesn't invent a repo" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" orphan)"
  git -C "$main" worktree remove --force "$dir"
  rm -rf "$main"
  cd "$TMP"; wt_run list
  [ "$status" -eq 0 ]
  # The old bug listed the CURRENT repo's branches again under a repo named ".".
  ! [[ "$output" == *" . "* ]]
}

@test "registry: parallel creates must not lose rows to a read-modify-write race" {
  # One repo EACH, deliberately. The contention under test is eight processes
  # writing one registry.tsv — but eight `git worktree add` in a single repo also
  # race each other, inside git (`failed to read .git/worktrees/<n>/commondir`),
  # and a checkout git dropped is indistinguishable here from a row holt lost.
  # That made this test flaky on Linux and, worse, able to mask the regression it
  # exists to catch. Separate repos remove git from the picture; the registry is
  # still the one shared file all eight are fighting over.
  local i repos=()
  for i in 1 2 3 4 5 6 7 8; do repos+=("$(mkrepo "par$i")"); done
  # The repos are built serially so the parallel section is nothing BUT the eight
  # concurrent registry writes.
  for i in 1 2 3 4 5 6 7 8; do
    hook_create "${repos[$((i - 1))]}" "par$i" >/dev/null 2>&1 &
  done
  wait
  [ "$(reg_rows)" -eq 8 ]
}

# ── bare PATH (the hook environment) ─────────────────────────────────────────
#
# Claude Code fires WorktreeCreate/WorktreeRemove with no PATH at all, and holt
# shells out to git for everything. This is the case that breaks at pane-open
# time — the worst moment to find it — so it gets its own tests rather than
# riding along inside another one.

@test "bare PATH: the create hook still resolves git" {
  local main out
  main="$(mkrepo alpha)"
  out="$(printf '{"name":"barepath","cwd":"%s"}' "$main" | env -u PATH "$WT" create 2>/dev/null)"
  [ "$out" = "$CLAUDE_WT_BASE/alpha/barepath" ]
  [ -e "$out/.git" ]
  [ "$(git -C "$out" branch --show-current)" = worktree-barepath ]
}

@test "bare PATH: the rescue is APPENDED, so test shims still win" {
  # If the rescue were prepended, the real gh under /run/current-system/sw/bin
  # would beat the shim and the whole suite would quietly test the machine.
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" shimwins)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$(git -C "$dir" rev-parse HEAD)" FAKE_GH_PR=12
  export FAKE_GH_BRANCH=worktree-shimwins
  commit_in "$dir" post.txt "past the merge"
  cd "$TMP"; wt_run
  [ "$status" -eq 0 ]
  [[ "$output" == *"live+1"* ]] || fail "the shim gh lost to a real one: $output"
}

# ── heartbeat / occupancy leases ─────────────────────────────────────────────
#
# `lsof` answers "is a process cwd'd in here?", which is the right question for
# a zellij pane and the wrong one for everything else. A lease is how a client
# that knows its own sessions says so directly. The asymmetry below is the whole
# point and is worth stating twice: a lease may SAVE a checkout from the sweep,
# never condemn one, because "nobody leased it" is not evidence that nobody is
# there. HOLT_OCCUPANCY=lease is the one deployment entitled to say otherwise.
#
# Every test here passes `--pid $$` explicitly. The default — the CALLING
# process — is right for the embedder that exec's holt and stays alive, and
# wrong under bats, where `run` forks a subshell that exits the moment holt
# does. Naming the test shell keeps the lease alive across the later `reap`,
# which is the situation being tested.

@test "heartbeat: a lease keeps a landed checkout from being reaped" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" leased)"
  git -C "$main" merge -q --no-edit worktree-leased
  wt_run heartbeat "$dir" --pid $$
  [ "$status" -eq 0 ]
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"kept leased (alpha) — something is standing in the checkout"* ]]
  # A lease knows the pid but never the command, so the provider stands in.
  [[ "$output" == *"pid $$ (leases)"* ]] || fail "$output"
  [ -e "$dir/.git" ]
}

@test "heartbeat: the default holder is the calling process, which bats then reaps" {
  # The complement of the tests above: with no --pid, the lease dies with the
  # short-lived shell `run` forked for it. That is the contract working — a
  # client that exits stops vouching for its worktree the instant it does.
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ephemeral)"
  git -C "$main" merge -q --no-edit worktree-ephemeral
  wt_run heartbeat "$dir"
  [ "$status" -eq 0 ]
  cd "$TMP"; wt_run reap
  [[ "$output" == *"reaped ephemeral (alpha)"* ]]
}

@test "heartbeat: --release drops the lease and the checkout reaps" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" transient)"
  git -C "$main" merge -q --no-edit worktree-transient
  wt_run heartbeat "$dir" --pid $$
  wt_run heartbeat "$dir" --release
  [ "$status" -eq 0 ]
  cd "$TMP"; wt_run reap
  [[ "$output" == *"reaped transient (alpha)"* ]]
  [ ! -e "$dir" ]
}

@test "heartbeat: a lease whose holder is gone protects nothing" {
  local main dir dead; main="$(mkrepo alpha)"; dir="$(mkwt "$main" ghost)"
  git -C "$main" merge -q --no-edit worktree-ghost
  # A pid we can prove is finished. The kernel is the witness here, not the
  # 90s TTL: a killed client must not hold its worktree hostage for a minute
  # and a half.
  sh -c 'exit 0' & dead=$!
  wait "$dead" 2>/dev/null || true
  wt_run heartbeat "$dir" --pid "$dead"
  [ "$status" -eq 0 ]
  cd "$TMP"; wt_run reap
  [[ "$output" == *"reaped ghost (alpha)"* ]]
}

@test "heartbeat: leases never vouch for an EMPTY checkout — no lsof still degrades" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" cautious)"
  git -C "$main" merge -q --no-edit worktree-cautious
  # A populated lease directory that says nothing about THIS worktree. The
  # temptation is to read that as "so it's free"; taking it would reap the
  # checkout of anyone who simply cd'd in without telling holt.
  local other; other="$(mkwt "$(mkrepo beta)" elsewhere)"
  wt_run heartbeat "$other" --pid $$
  export FAKE_LSOF_BROKEN=1
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"no lsof"* ]]
  [ -e "$dir/.git" ]
}

@test "heartbeat: HOLT_OCCUPANCY=lease lets an embedder answer for absence" {
  # Two repos, not two worktrees of one: mkwt commits the same work.txt in each,
  # so landing both branches into a single main is an add/add conflict rather
  # than the fixture this test wants.
  local alpha beta held free
  alpha="$(mkrepo alpha)"; beta="$(mkrepo beta)"
  held="$(mkwt "$alpha" held)"; free="$(mkwt "$beta" free)"
  git -C "$alpha" merge -q --no-edit worktree-held
  git -C "$beta" merge -q --no-edit worktree-free
  wt_run heartbeat "$held" --pid $$
  # No lsof at all — the deployment this models has no processes to scan. The
  # embedder owns every session, so its leases are the whole truth.
  export FAKE_LSOF_BROKEN=1 HOLT_OCCUPANCY=lease
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" != *"no lsof"* ]] || fail "leases should have answered: $output"
  [[ "$output" == *"reaped free (beta)"* ]]
  [[ "$output" == *"kept held (alpha) — something is standing in the checkout"* ]]
  [ -e "$held/.git" ]
}

@test "heartbeat: a leased checkout reads as occupied in --json" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" watched)"
  wt_run heartbeat "$dir" --pid $$
  export FAKE_LSOF_BROKEN=1     # the lease is the ONLY signal left
  cd "$TMP"; wt_run list --json
  [ "$status" -eq 0 ]
  [[ "$output" == *'"occupied": true'* ]] || fail "$output"
}

@test "heartbeat: refusing a path that does not exist beats inventing a lease" {
  wt_run heartbeat "$TMP/nowhere"
  [ "$status" -eq 1 ]
  [[ "$output" == *"no such path"* ]]
}

# ── dispatch ─────────────────────────────────────────────────────────────────

@test "dispatch: --help prints the header block, including park/unpark" {
  wt_run --help
  [ "$status" -eq 0 ]
  [[ "$output" == *"holt park [label]"* ]]
  [[ "$output" == *"holt unpark"* ]]
  [[ "$output" != *"#!/usr/bin/env"* ]]
}

@test "dispatch: a bare unknown token is treated as a lane name" {
  wt_run gibberish
  [ "$status" -ne 0 ]
  [[ "$output" == *"no lane named 'gibberish'"* ]]
}

# Bare `holt` IS the listing, so `holt --json` must be the machine-readable
# listing — not "unknown flag", which is what every consumer that reached for
# the obvious spelling used to get. It has to be the SAME envelope as the
# explicit form, or the two spellings drift and consumers pick the wrong one.
@test "dispatch: bare --json is the listing, byte-identical to 'list --json'" {
  local main; main="$(mkrepo alpha)"
  mkwt "$main" sparkle >/dev/null

  wt_run --json
  [ "$status" -eq 0 ]
  [[ "$output" != *"unknown flag"* ]]
  [[ "$output" == *'"lanes"'* ]]
  [[ "$output" == *'"name": "sparkle"'* ]]

  local bare="$output"
  wt_run list --json
  [ "$status" -eq 0 ]
  [ "$output" = "$bare" ] || fail "the two spellings disagree"
}

# ── policy seams ─────────────────────────────────────────────────────────────
#
# Every one of these asserts the same two halves of the same contract, on a
# different decision: with no hook configured, holt behaves EXACTLY as it did
# before hooks existed (the whole rest of this suite is that half); with a hook
# configured, the hook's answer is the answer, including when it contradicts
# holt's own. The second half is the product — a machine has to be able to be
# right about its own lanes when holt is wrong.

mkhook() { # mkhook <name> <body> — an executable hook, echo its path
  local path="$TMP/hooks/$1"
  mkdir -p "$TMP/hooks"
  printf '#!/usr/bin/env bash\n%s\n' "$2" >"$path"
  chmod +x "$path"
  printf '%s' "$path"
}

setcfg() { # setcfg <toml body> — plant the machine config
  mkdir -p "$XDG_CONFIG_HOME/holt"
  printf '%s\n' "$1" >"$XDG_CONFIG_HOME/holt/config.toml"
}

@test "hooks: no config means every decision is holt's own" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" plain)"
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"nothing to reap"* ]]      # unmerged: holt's own rule held
  [ -e "$dir/.git" ]
}

@test "hooks: landed — a branch holt calls unmerged is reaped when the hook says landed" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" trainlanded)"
  hook="$(mkhook landed 'echo "{\"via\": \"release-train\"}"; exit 0')"
  setcfg "[hooks]
landed = \"$hook\""
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"reaped trainlanded (alpha)"* ]] || fail "the landed hook did not decide: $output"
  [ ! -e "$dir" ]
  run git -C "$main" show-ref -q --verify refs/heads/worktree-trainlanded
  [ "$status" -ne 0 ]
}

@test "hooks: landed — a hook that says no keeps a branch git itself calls merged" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" heldback)"
  git -C "$main" merge -q --no-edit worktree-heldback   # ancestry-merged: holt would reap
  hook="$(mkhook landed 'exit 1')"
  setcfg "[hooks]
landed = \"$hook\""
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [ -e "$dir/.git" ]
  git -C "$main" show-ref -q --verify refs/heads/worktree-heldback
}

@test "hooks: landed — exit 3 defers, leaving holt's own ladder in force" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" deferred)"
  git -C "$main" merge -q --no-edit worktree-deferred
  hook="$(mkhook landed 'exit 3')"
  setcfg "[hooks]
landed = \"$hook\""
  cd "$TMP"; wt_run reap
  [[ "$output" == *"reaped deferred (alpha)"* ]] || fail "defer did not fall through: $output"
}

@test "hooks: landed — a hook that cannot run warns and falls back, never fails" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" broken)"
  git -C "$main" merge -q --no-edit worktree-broken
  setcfg '[hooks]
landed = "/nonexistent/holt-landed"'
  cd "$TMP"; wt_run reap
  [ "$status" -eq 0 ]
  [[ "$output" == *"wouldn't run"* ]] || fail "a dead hook must say so: $output"
  [[ "$output" == *"reaped broken (alpha)"* ]] || fail "a dead hook must not cost the sweep: $output"
}

@test "hooks: preserve — it decides whether a closing pane's dirt becomes a wip commit" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" noswap)"
  echo edit >"$dir/README.md"            # a TRACKED edit: holt would always preserve
  hook="$(mkhook preserve 'exit 1')"
  setcfg "[hooks]
preserve = \"$hook\""
  hook_remove "$dir" 2>/dev/null
  run git -C "$main" log -1 --format=%s worktree-noswap
  [[ "$output" != wip:* ]] || fail "the hook said don't preserve and holt did anyway: $output"
}

@test "hooks: preserve — a yes wip-commits scratch holt would have dropped" {
  local main dir tip hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" keepall)"
  tip="$(git -C "$dir" rev-parse HEAD)"
  export FAKE_GH_MERGED=1 FAKE_GH_OID="$tip"     # landed; only untracked left
  touch "$dir/scratch.o"
  hook="$(mkhook preserve 'exit 0')"
  setcfg "[hooks]
preserve = \"$hook\""
  hook_remove "$dir" 2>/dev/null
  run git -C "$main" log -1 --format=%s worktree-keepall
  [[ "$output" == wip:* ]] || fail "the hook said preserve and holt dropped it: $output"
}

@test "hooks: a predicate is handed the situation as HOLT_* vars AND as JSON" {
  # Both channels carry the same table, because a seam may be a program with a
  # JSON parser or three lines of shell, and neither should have to become the
  # other. HOLT_BASE_BRANCH is the one that needs pinning: HOLT_BASE is already
  # the lane base DIRECTORY, so the default branch had to be spelled apart.
  #
  # The hook only DUMPS its two inputs; every assertion lives out here in bats.
  # The first version matched the JSON with a `case` nested inside a command
  # substitution inside the hook body, and word-split its own printf arguments
  # on both CI runners while passing on two local shells — including the exact
  # bash 3.2 macOS ships. Whatever the trigger, a hook body that only writes
  # two files cannot have it: no nested quoting, no command substitution, and
  # printf rather than echo, which is the one that mangles a JSON \n.
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" payload)"
  echo edit >"$dir/README.md"
  hook="$(mkhook preserve '
    read -r body
    printf "%s\n" "$body" >"'"$TMP"'/stdin"
    printf "hook=%s name=%s branch=%s repo=%s base=%s main=%s\n" \
      "$HOLT_HOOK" "$HOLT_NAME" "$HOLT_BRANCH" "$HOLT_REPO" \
      "$HOLT_BASE_BRANCH" "$HOLT_MAIN" >"'"$TMP"'/env"
    exit 3')"
  setcfg "[hooks]
preserve = \"$hook\""
  hook_remove "$dir" 2>/dev/null

  run cat "$TMP/env"
  [ "$output" = "hook=preserve name=payload branch=worktree-payload repo=acme/alpha base=main main=$main" ] \
    || fail "the HOLT_* vars are wrong: $output"

  run cat "$TMP/stdin"
  local json="$output" needle
  for needle in '"branch":"worktree-payload"' '"base":"main"' '"repo":"acme/alpha"'; do
    [[ "$json" == *"$needle"* ]] || fail "stdin JSON lacks $needle: $json"
  done
}

@test "hooks: resume — the hook reopens the session instead of holt exec'ing a client" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" paned)"
  hook="$(mkhook resume '
    printf "%s %s %s\n" "$HOLT_NAME" "$HOLT_PATH" "$HOLT_LANE_AGENT" >"'"$TMP"'/opened"
    printf "%s\n" "$HOLT_COMMAND" >"'"$TMP"'/cmd"
    exit 0')"
  setcfg "[hooks]
resume = \"$hook\""
  cd "$TMP"; wt_run paned
  [ "$status" -eq 0 ]
  run cat "$TMP/opened"
  [ "$output" = "paned $dir claude" ] || fail "resume payload is wrong: $output"
  # A hook that spawns a pane runs what holt WOULD have run, or the pane lands
  # on the picker holt just spared the user.
  [ "$(cat "$TMP/cmd")" = "claude --continue" ] || fail "wrong command: $(cat "$TMP/cmd")"
}

@test "hooks: resume — the checkout is rebuilt BEFORE the hook is asked to open it" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" rebuilt)"
  hook_remove "$dir" >/dev/null 2>&1           # park it: branch survives, checkout gone
  [ ! -e "$dir" ]
  hook="$(mkhook resume 'test -e "$HOLT_PATH/.git" && echo rebuilt >"'"$TMP"'/state"; exit 0')"
  setcfg "[hooks]
resume = \"$hook\""
  cd "$TMP"; wt_run rebuilt
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/state" 2>/dev/null)" = rebuilt ] || fail "the hook was handed a checkout that isn't there"
}

@test "hooks: resume — a spawned lane tells the hook where the CHAT lives" {
  local main sub parent child hook
  main="$(mkrepo alpha)"; sub="$(mkrepo beta)"
  parent="$(mkwt "$main" workshop)"
  mkdir -p "$HOME/.claude/projects/$(printf '%s' "$parent" | tr './' '--')"
  cd "$parent"; child="$("$WT" child "$sub" workshop 2>/dev/null)"
  hook="$(mkhook resume 'printf "%s|%s\n" "$HOLT_PATH" "$HOLT_CHAT" >"'"$TMP"'/chat"; exit 0')"
  setcfg "[hooks]
resume = \"$hook\""
  cd "$TMP"; wt_run beta/workshop
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/chat")" = "$child|$parent" ] \
    || fail "a hook opening a pane in \$HOLT_PATH would get an empty session: $(cat "$TMP/chat")"
}

@test "hooks: resume — a hook that refuses exits 2, not 0" {
  local main dir hook; main="$(mkrepo alpha)"; dir="$(mkwt "$main" refused)"
  hook="$(mkhook resume 'exit 2')"
  setcfg "[hooks]
resume = \"$hook\""
  cd "$TMP"; wt_run refused
  [ "$status" -eq 2 ] || fail "a safety refusal must stay distinguishable from a usage error: $status"
}

@test "hooks: open — a fresh lane's session is the machine's business too" {
  local main hook; main="$(mkrepo alpha)"
  hook="$(mkhook open 'printf "%s %s\n" "$HOLT_NAME" "$HOLT_LANE_AGENT" >"'"$TMP"'/opened"; exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  cd "$main"; wt_run new fresh --open
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/opened")" = "fresh claude" ] || fail "open payload is wrong: $(cat "$TMP/opened")"
  [ -e "$CLAUDE_WT_BASE/alpha/fresh/.git" ]
}

# ── --prompt: a lane that opens already knowing the task ─────────────────────

# A client that reports its own argv, one element per line. Deliberately local
# to these tests rather than a setup() shim: several tests elsewhere assert what
# happens when NO client is installed, and a global `claude` would quietly make
# those pass for the wrong reason.
mkclient() { # mkclient <id> — write a reporting shim into $BIN, echo nothing
  cat >"$BIN/$1" <<'EOF'
#!/usr/bin/env bash
for a in "$@"; do printf '<%s>\n' "$a"; done
EOF
  chmod +x "$BIN/$1"
}

@test "prompt: spawn hands the open hook the client's START invocation, not its bare open" {
  local b hook; b="$(mkrepo beta)"
  hook="$(mkhook open 'printf "%s\n" "$HOLT_COMMAND" >"'"$TMP"'/cmd"; exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  run bash -c "'$WT' spawn '$b' notch-flicker --prompt 'fix the notch' 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$output" = "$CLAUDE_WT_BASE/beta/notch-flicker" ]   # the path is still the stdout contract
  # `--` before the prompt, always: a task beginning with a dash is a FLAG to
  # commander and clap otherwise, and dies before the pane draws anything.
  [ "$(cat "$TMP/cmd")" = "claude -- 'fix the notch'" ] || fail "wrong invocation: $(cat "$TMP/cmd")"
}

@test "prompt: a multi-line prompt with quotes and a leading dash survives HOLT_COMMAND" {
  # The regression this exists for: `command` used to be a bare space-join of
  # argv. Every invocation holt had ever handed a hook was one or two bare words
  # ("claude", "codex resume --last"), so the bug was invisible — and the first
  # prompt through it would shatter into words, or unbalance the opener's shell.
  local b hook brief; b="$(mkrepo beta)"
  mkclient claude
  brief="- rewrite \"the parser\"
  Next: run \$HOME/x.sh  # don't expand me"
  hook="$(mkhook open 'bash -c "$HOLT_COMMAND" >"'"$TMP"'/argv" 2>&1; exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  run bash -c "'$WT' spawn '$b' parser --prompt \"\$1\" 2>/dev/null" _ "$brief"
  [ "$status" -eq 0 ]
  # ONE argument after `--`, byte-identical to what went in — newline, quotes,
  # leading dash and unexpanded `$HOME` alike. (The shim reports `$@`, so the
  # client's own name is not in there.)
  [ "$(cat "$TMP/argv")" = "$(printf '<-->\n<%s>' "$brief")" ] \
    || fail "the prompt did not survive the round trip:
$(cat "$TMP/argv")"
}

@test "prompt: spawn with no open hook is degraded, not failed — the lane exists" {
  local b; b="$(mkrepo beta)"
  run bash -c "'$WT' spawn '$b' orphan --prompt 'do the thing'"
  # 3, not 1: holt made the lane it was asked for. What was unavailable is
  # somewhere to open it, and the caller needs to tell those two apart.
  [ "$status" -eq 3 ]
  [ -e "$CLAUDE_WT_BASE/beta/orphan/.git" ]
  [[ "$output" == *"claude -- 'do the thing'"* ]] || fail "no recovery command: $output"
}

@test "prompt: --prompt-file reads the brief from a file, and - from stdin" {
  local b hook; b="$(mkrepo beta)"
  hook="$(mkhook open 'printf "%s\n" "$HOLT_COMMAND" >"'"$TMP"'/cmd"; exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  printf 'ship the thing\n' >"$TMP/brief.md"        # trailing newline is not the task

  run bash -c "'$WT' spawn '$b' from-file --prompt-file '$TMP/brief.md' 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/cmd")" = "claude -- 'ship the thing'" ] || fail "from file: $(cat "$TMP/cmd")"

  run bash -c "'$WT' spawn '$b' from-stdin --prompt-file - <'$TMP/brief.md' 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/cmd")" = "claude -- 'ship the thing'" ] || fail "from stdin: $(cat "$TMP/cmd")"
}

@test "prompt: an empty --prompt-file is a usage error, not a lane opened on nothing" {
  local b; b="$(mkrepo beta)"
  : >"$TMP/empty.md"
  run "$WT" spawn "$b" nothing --prompt-file "$TMP/empty.md"
  [ "$status" -eq 1 ]
  [ ! -e "$CLAUDE_WT_BASE/beta/nothing" ] || fail "a lane was created for a prompt that isn't there"
  run "$WT" spawn "$b" nofile --prompt-file "$TMP/does-not-exist.md"
  [ "$status" -eq 1 ]
}

@test "prompt: spawn WITHOUT a prompt never fires the open hook" {
  # The pairing that makes the test above mean something. `holt spawn` on its
  # own is still "make me a lane and print its path" — a caller doing its own
  # opening must not get a second window from holt.
  local b hook; b="$(mkrepo beta)"
  hook="$(mkhook open 'echo fired >"'"$TMP"'/fired"; exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  run bash -c "'$WT' spawn '$b' quiet 2>/dev/null"
  [ "$status" -eq 0 ]
  [ ! -e "$TMP/fired" ] || fail "spawn opened a window nobody asked for"
}

@test "prompt: an open hook that FAILS is degraded too, not a usage error" {
  # These used to be two answers to one situation: no hook exited 3 with a
  # recovery line, a hook that broke exited 1 with none. A palette command whose
  # window manager was down read 1 as "you asked wrong", retried, and made a
  # SECOND lane while the first sat on disk.
  local b hook; b="$(mkrepo beta)"
  hook="$(mkhook open 'exit 7')"      # 7 means nothing to holt — a broken hook
  setcfg "[hooks]
open = \"$hook\""
  run bash -c "'$WT' spawn '$b' broken --prompt 'do the thing'"
  [ "$status" -eq 3 ] || fail "a broken opener must not read as a bad invocation: $status"
  [ -e "$CLAUDE_WT_BASE/beta/broken/.git" ]
  [[ "$output" == *"claude -- 'do the thing'"* ]] || fail "no recovery command: $output"
}

@test "prompt: an open hook that REFUSES still exits 2" {
  # A decision, not a breakage — and a caller has to tell them apart.
  local b hook; b="$(mkrepo beta)"
  hook="$(mkhook open 'exit 2')"
  setcfg "[hooks]
open = \"$hook\""
  run bash -c "'$WT' spawn '$b' declined --prompt 'do the thing'"
  [ "$status" -eq 2 ]
}

@test "prompt: an empty positional is refused instead of shifting the next one along" {
  # `holt spawn "$repo" "" claude` used to fall through the name slot and name
  # the lane "claude". Every SDK passes the name positionally, so an unset
  # variable in a caller silently produced a misnamed lane.
  local b; b="$(mkrepo beta)"
  run "$WT" spawn "$b" "" claude
  [ "$status" -eq 1 ]
  [ ! -e "$CLAUDE_WT_BASE/beta/claude" ] || fail "the agent id became the lane name"
}

@test "prompt: an empty --prompt is refused, like an empty --prompt-file" {
  local b main; b="$(mkrepo beta)"; main="$(mkrepo alpha)"
  run "$WT" spawn "$b" blank --prompt ""
  [ "$status" -eq 1 ]
  [ ! -e "$CLAUDE_WT_BASE/beta/blank" ]
  cd "$main"; wt_run new blank --prompt "   "
  [ "$status" -eq 1 ]
  [ ! -e "$CLAUDE_WT_BASE/alpha/blank" ]
}

@test "prompt: --image with no first turn to look at it is refused" {
  # Silently dropping it opens a pane whose agent was never given the screenshot
  # the user pointed at.
  local main b; main="$(mkrepo alpha)"; b="$(mkrepo beta)"
  : >"$TMP/shot.png"
  cd "$main"; wt_run new shotless --open --image "$TMP/shot.png"
  [ "$status" -eq 1 ]
  [ ! -e "$CLAUDE_WT_BASE/alpha/shotless" ]
  run "$WT" spawn "$b" shotless --image "$TMP/shot.png"
  [ "$status" -eq 1 ]
}

@test "prompt: --image reaches a client that can attach it, and is described to one that can't" {
  local b hook; b="$(mkrepo beta)"
  : >"$TMP/shot.png"
  hook="$(mkhook open 'printf "%s\n" "$HOLT_COMMAND" >"'"$TMP"'/cmd"; exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  run bash -c "'$WT' spawn '$b' shot --agent codex --prompt 'look' --image '$TMP/shot.png' 2>/dev/null"
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/cmd")" = "codex -i $TMP/shot.png -- look" ] || fail "codex: $(cat "$TMP/cmd")"

  # claude has no image flag, so the path is named in the prompt instead of
  # being dropped — an agent reasoning about a screenshot it was never given is
  # worse than one told where to find it.
  run bash -c "'$WT' spawn '$b' shot-claude --prompt 'look' --image '$TMP/shot.png' 2>/dev/null"
  [ "$status" -eq 0 ]
  [[ "$(cat "$TMP/cmd")" == *"A screenshot for this task is at $TMP/shot.png"* ]] \
    || fail "claude: $(cat "$TMP/cmd")"
}

@test "prompt: new --prompt implies --open, and keeps the lane's own client" {
  local main hook; main="$(mkrepo alpha)"
  hook="$(mkhook open 'printf "%s|%s\n" "$HOLT_LANE_AGENT" "$HOLT_COMMAND" >"'"$TMP"'/cmd"; exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  # No --open anywhere: a prompt with no session to hand it to is a prompt
  # nobody reads, so it opens one.
  cd "$main"; wt_run new tasked --agent codex --prompt "look at the bar"
  [ "$status" -eq 0 ]
  [ "$(cat "$TMP/cmd")" = "codex|codex -- 'look at the bar'" ] || fail "wrong: $(cat "$TMP/cmd")"
  [ "$(awk -F'\t' '$1=="tasked"{print $6}' "$REG")" = codex ]
}

@test "prompt: --cmd and --prompt are refused together, before anything is created" {
  local main; main="$(mkrepo alpha)"
  cd "$main"; wt_run new clash --cmd 'echo hi' --prompt 'do it'
  [ "$status" -eq 1 ]
  [ ! -e "$CLAUDE_WT_BASE/alpha/clash" ]
}

# ── namer: a lane named after its task ───────────────────────────────────────
#
# Opt-in, cosmetic, and unable to fail a lane. Every test here is one half of
# that: what happens with no `namer` key (nothing — exactly the behaviour that
# predates the key), and what happens when the namer misbehaves (a warning and
# a random name, never a lane that didn't get made). The namer's output is a
# model's text on its way to a branch name and a path, so the last two are the
# gate that stands between them.

mknamer() { # mknamer <body> — a namer shim, and the `fake` adapter that runs it
  mkdir -p "$TMP/hooks" "$XDG_CONFIG_HOME/holt/adapters/namer"
  printf '#!/usr/bin/env bash\n%s\n' "$1" >"$TMP/hooks/namer"
  chmod +x "$TMP/hooks/namer"
  printf 'kind = "namer"\nid = "fake"\nname = ["%s", "{{.Prompt}}"]\n' "$TMP/hooks/namer" \
    >"$XDG_CONFIG_HOME/holt/adapters/namer/fake.toml"
}

lane_name() { # lane_name <path> — the basename holt chose
  basename "$1"
}

@test "namer: with no namer configured nothing runs, and an unnamed lane is still a random pair" {
  local b; b="$(mkrepo beta)"
  mknamer 'touch "'"$TMP"'/ran"; echo mobile-nav-jitter'   # armed, but not named in the config
  run bash -c "'$WT' spawn '$b' --prompt 'the bar draws a draft PR in the merged colour' 2>/dev/null"
  [ "$status" -eq 3 ]                      # no open hook — the lane exists, nothing opened it
  [ ! -e "$TMP/ran" ] || fail "the namer ran with no namer key in the config"
  [ -d "$output" ] || fail "no lane at $output"
  [[ "$(lane_name "$output")" =~ ^[a-z]+-[a-z]+$ ]] || fail "not a random pair: $(lane_name "$output")"
}

@test "namer: a configured namer names the lane, and is told the task, the repo and the neighbours" {
  local b; b="$(mkrepo beta)"
  mkwt "$b" tart-backend                   # a neighbour for the namer to avoid
  mknamer 'printf "%s" "$1" >"'"$TMP"'/req"; echo mobile-nav-jitter'
  setcfg 'namer = "fake"'
  run bash -c "'$WT' spawn '$b' --prompt 'the bar draws a draft PR in the merged colour' 2>/dev/null"
  [ "$status" -eq 3 ]
  [ "$output" = "$CLAUDE_WT_BASE/beta/mobile-nav-jitter" ] || fail "lane is at $output"
  [ -d "$CLAUDE_WT_BASE/beta/mobile-nav-jitter" ]
  git -C "$b" show-ref -q --verify refs/heads/worktree-mobile-nav-jitter

  run cat "$TMP/req"
  [[ "$output" == *"the bar draws a draft PR in the merged colour"* ]] || fail "no task: $output"
  [[ "$output" == *"acme/beta"* ]] || fail "no repo: $output"
  # The neighbours are in there because holt's own collision handling is a
  # numeric suffix, and `fix-mobile-2` is correct and unreadable.
  [[ "$output" == *"tart-backend"* ]] || fail "no neighbours: $output"
}

@test "namer: a name the caller gave always wins, and a lane with no task never asks" {
  local b main; b="$(mkrepo beta)"; main="$(mkrepo alpha)"
  mknamer 'touch "'"$TMP"'/ran"; echo mobile-nav-jitter'
  setcfg 'namer = "fake"'

  run bash -c "'$WT' spawn '$b' notch-flicker --prompt 'fix the notch' 2>/dev/null"
  [ "$output" = "$CLAUDE_WT_BASE/beta/notch-flicker" ] || fail "the namer overrode a given name: $output"
  [ ! -e "$TMP/ran" ] || fail "the namer ran for a lane that already had a name"

  # No task, nothing to name after: `holt new` on its own must not start a
  # process, which is the whole reason the ⌘↵ path is unaffected by this.
  cd "$main"; wt_run new
  [ "$status" -eq 0 ]
  [ ! -e "$TMP/ran" ] || fail "the namer ran for a lane with no prompt"
}

@test "namer: an answer that isn't a name warns and falls back — the lane is still made" {
  local b; b="$(mkrepo beta)"
  mknamer 'echo "I would need more detail about what you want changed."'
  setcfg 'namer = "fake"'
  run bash -c "'$WT' spawn '$b' --prompt 'fix the notch' 2>'$TMP/err'"
  [ "$status" -eq 3 ]
  [ -d "$output" ] || fail "a bad name cost the lane"
  [[ "$(lane_name "$output")" =~ ^[a-z]+-[a-z]+$ ]] || fail "prose became a name: $(lane_name "$output")"
  grep -q "isn't a name" "$TMP/err" || fail "silent fallback: $(cat "$TMP/err")"
}

@test "namer: a namer that cannot run at all warns and falls back, never fails" {
  local b; b="$(mkrepo beta)"

  # Named in the config, no adapter file on disk.
  setcfg 'namer = "ollama"'
  run bash -c "'$WT' spawn '$b' --prompt 'fix the notch' 2>'$TMP/err'"
  [ "$status" -eq 3 ]
  [ -d "$output" ] || fail "a missing adapter cost the lane"
  grep -q "ollama" "$TMP/err" || fail "the missing adapter went unnamed: $(cat "$TMP/err")"

  # An adapter file whose command isn't installed.
  mkdir -p "$XDG_CONFIG_HOME/holt/adapters/namer"
  printf 'kind = "namer"\nid = "gone"\nname = ["holt-namer-not-installed", "{{.Prompt}}"]\n' \
    >"$XDG_CONFIG_HOME/holt/adapters/namer/gone.toml"
  setcfg 'namer = "gone"'
  run bash -c "'$WT' spawn '$b' --prompt 'fix the notch' 2>'$TMP/err'"
  [ "$status" -eq 3 ]
  [ -d "$output" ] || fail "an uninstalled namer cost the lane"
  grep -q "PATH" "$TMP/err" || fail "the dead namer went unexplained: $(cat "$TMP/err")"
}

@test "namer: nothing a namer prints can escape the lane base or name a flag" {
  local b; b="$(mkrepo beta)"
  setcfg 'namer = "fake"'
  local answer
  for answer in '../../../etc/passwd' '-rf' '..' '/tmp/holt-namer-escape' 'a; rm -rf ~' 'beta'; do
    mknamer "printf '%s\n' '$answer'"
    run bash -c "'$WT' spawn '$b' --prompt 'fix the notch' 2>/dev/null"
    [ "$status" -eq 3 ] || fail "spawn failed on answer '$answer': $output"
    [[ "$(dirname "$output")" = "$CLAUDE_WT_BASE/beta" ]] || fail "'$answer' escaped to $output"
    [[ "$(lane_name "$output")" =~ ^[a-z]+-[a-z]+$ ]] || fail "'$answer' became $(lane_name "$output")"
  done
  [ ! -e "/tmp/holt-namer-escape" ]
  # …and the repo naming itself is dropped rather than spending a word on what
  # every listing already shows.
  mknamer 'echo beta-nav-jitter'
  run bash -c "'$WT' spawn '$b' --prompt 'fix the notch' 2>/dev/null"
  [ "$(lane_name "$output")" = "nav-jitter" ] || fail "the repo named itself: $(lane_name "$output")"
}

@test "namer: the namer's stdin is empty, never the brief holt just read off fd 0" {
  # `--prompt-file -` drains stdin to read the brief, and the client is handed a
  # terminal back afterwards. A namer that inherited fd 0 would either consume
  # what is left or block on a pipe nobody is writing to — a client that waits
  # on a non-tty stdin adds seconds to every single spawn.
  local b; b="$(mkrepo beta)"
  printf 'make the bar draw draft PRs in grey\n' >"$TMP/brief.md"
  mknamer 'cat >"'"$TMP"'/stdin"; echo draft-pr-grey'
  setcfg 'namer = "fake"'
  run bash -c "'$WT' spawn '$b' --prompt-file - <'$TMP/brief.md' 2>/dev/null"
  [ "$status" -eq 3 ]
  [ "$output" = "$CLAUDE_WT_BASE/beta/draft-pr-grey" ] || fail "lane is at $output"
  [ ! -s "$TMP/stdin" ] || fail "the namer was handed holt's stdin: $(cat "$TMP/stdin")"
}

@test "hooks: a lane's own fields never shadow holt's own environment" {
  # Every HOLT_* a hook is given leaks into the pane that hook spawns, and into
  # every window opened from it. So no field may be spelled as a variable holt
  # itself reads: HOLT_STATE is the state DIRECTORY and HOLT_AGENT is the
  # one-invocation client override, which is why the lane's are HOLT_LANE_STATE
  # and HOLT_LANE_AGENT. When HOLT_STATE carried the lane's state, `holt reap`
  # in an agent pane wrote the machine's reap ledger to ./live/reaped.log,
  # inside whatever git checkout the pane was sitting in.
  local main hook; main="$(mkrepo alpha)"
  hook="$(mkhook open '
    printf "state=%s agent=%s\n" "$HOLT_LANE_STATE" "$HOLT_LANE_AGENT" >"'"$TMP"'/lane"
    printf "state=%s agent=%s\n" "$HOLT_STATE" "$HOLT_AGENT" >"'"$TMP"'/shadow"
    exit 0')"
  setcfg "[hooks]
open = \"$hook\""
  cd "$main"; wt_run new noshadow --open
  [ "$status" -eq 0 ]

  run cat "$TMP/lane"
  [ "$output" = "state=live agent=claude" ] || fail "the lane's own fields are wrong: $output"

  # holt's own two must arrive untouched by the lane — empty here, because the
  # suite sets neither. A lane value in either is the bug.
  run cat "$TMP/shadow"
  [ "$output" = "state= agent=" ] || fail "a lane field shadowed holt's own environment: $output"
}

@test "hooks: agent — the default client can be a program, not just a constant" {
  local main hook; main="$(mkrepo alpha)"
  hook="$(mkhook agent 'echo "{\"agent\": \"codex\"}"; exit 0')"
  setcfg "agent = \"claude\"

[hooks]
agent = \"$hook\""
  cd "$main"; wt_run agent default
  [ "$status" -eq 0 ]
  [ "$output" = codex ] || fail "the agent hook lost to the static key: $output"
}

# ── watch ────────────────────────────────────────────────────────────────────
#
# `holt watch --json` runs forever, so the suite's usual `run` — which waits
# for the process to exit before making $output/$status available — can't
# drive it. These helpers stand in: start it in the background, poll its
# output file until at least N lines have landed or a timeout passes, then
# always stop it in `teardown` — including when an assertion fails partway,
# so one red test never leaves a `holt watch` running loose past the suite.
#
# WATCH_TIMEOUT is generous on purpose: fsnotify's underlying primitive
# (kqueue on macOS, inotify on Linux) is normally sub-second, but a loaded CI
# runner is not the machine that number was measured on, and a slow pass
# beats a flaky one. watch_wait_lines returns the moment the line count is
# met, so this only costs real time on a genuine failure.
WATCH_TIMEOUT=8

watch_start() { # watch_start [more args...] — background `holt watch --json`; sets $WATCH_OUT
  WATCH_OUT="$TMP/watch-$BATS_TEST_NUMBER.out"
  : >"$WATCH_OUT"
  "$WT" watch --json "$@" >"$WATCH_OUT" 2>"$TMP/watch-$BATS_TEST_NUMBER.err" &
  WATCH_PID="$!"
}

watch_wait_lines() { # watch_wait_lines <n> <timeout-seconds> — block until $WATCH_OUT has n lines
  local n="$1" deadline=$((SECONDS + $2))
  while [ "$(wc -l <"$WATCH_OUT" 2>/dev/null | tr -d ' ')" -lt "$n" ]; do
    [ "$SECONDS" -lt "$deadline" ] || return 1
    sleep 0.05
  done
  return 0
}

watch_line() { sed -n "${1}p" "$WATCH_OUT"; }                                    # watch_line <n>
watch_kind() { watch_line "$1" | sed -n 's/.*"kind":"\([^"]*\)".*/\1/p'; }        # watch_kind <n>

# bats allows exactly one teardown per file; every other test in this suite
# needs nothing beyond $TMP going away with it, so this only ever has watch's
# background process to reap.
teardown() {
  if [ -n "${WATCH_PID:-}" ]; then
    kill "$WATCH_PID" 2>/dev/null
    wait "$WATCH_PID" 2>/dev/null
    WATCH_PID=""
  fi
}

@test "watch: an unknown flag refuses instead of starting a stream" {
  run "$WT" watch --bogus
  [ "$status" -ne 0 ]
  [[ "$output" == *"unknown flag"* ]]
}

@test "watch: hello carries holt+schema+capabilities, then syncs the existing lane, then ready" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" sparkle >/dev/null
  cd "$TMP"; watch_start
  watch_wait_lines 3 "$WATCH_TIMEOUT" || fail "stream never reached 3 lines: $(cat "$WATCH_OUT")"

  [ "$(watch_kind 1)" = hello ] || fail "line 1 wasn't hello: $(watch_line 1)"
  [[ "$(watch_line 1)" == *'"schema":1'* ]] || fail "hello carries no schema: $(watch_line 1)"
  [[ "$(watch_line 1)" == *'"capabilities":["registry"]'* ]] \
    || fail "hello carries no capabilities: $(watch_line 1)"

  [ "$(watch_kind 2)" = sync ] || fail "line 2 wasn't sync: $(watch_line 2)"
  [[ "$(watch_line 2)" == *'"source":"registry"'* ]] || fail "sync names no source: $(watch_line 2)"
  [[ "$(watch_line 2)" == *'"name":"sparkle"'* ]] \
    || fail "sync's lane isn't the --json envelope's shape: $(watch_line 2)"

  [ "$(watch_kind 3)" = ready ] || fail "line 3 wasn't ready: $(watch_line 3)"
}

@test "watch: an empty registry goes straight from hello to ready — no phantom lane" {
  cd "$TMP"; watch_start
  watch_wait_lines 2 "$WATCH_TIMEOUT" || fail "hello/ready never landed: $(cat "$WATCH_OUT")"
  [ "$(watch_kind 1)" = hello ]
  [ "$(watch_kind 2)" = ready ]
  sleep 0.5   # give a false-positive sync every chance to show up before asserting its absence
  [ "$(wc -l <"$WATCH_OUT" | tr -d ' ')" -eq 2 ] \
    || fail "an empty registry produced a line beyond hello/ready: $(cat "$WATCH_OUT")"
}

@test "watch: a new lane appears as created" {
  local main; main="$(mkrepo alpha)"
  cd "$TMP"; watch_start
  watch_wait_lines 2 "$WATCH_TIMEOUT" || fail "hello/ready never landed"

  mkwt "$main" fresh >/dev/null

  watch_wait_lines 3 "$WATCH_TIMEOUT" || fail "the new lane never reached the stream: $(cat "$WATCH_OUT")"
  [ "$(watch_kind 3)" = created ] || fail "line 3 wasn't created: $(watch_line 3)"
  [[ "$(watch_line 3)" == *'"name":"fresh"'* ]]
  [[ "$(watch_line 3)" == *'"state":"live"'* ]]
}

@test "watch: a pane closing on an unlanded branch appears as parked" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" closing)"
  cd "$TMP"; watch_start
  watch_wait_lines 3 "$WATCH_TIMEOUT" || fail "sync of the pre-existing lane never landed"

  hook_remove "$dir" 2>/dev/null

  watch_wait_lines 4 "$WATCH_TIMEOUT" || fail "the park never reached the stream: $(cat "$WATCH_OUT")"
  [ "$(watch_kind 4)" = parked ] || fail "line 4 wasn't parked: $(watch_line 4)"
  [[ "$(watch_line 4)" == *'"state":"parked"'* ]]
}

@test "watch: holt <name> on a parked lane appears as resumed" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" comeback)"
  hook_remove "$dir" 2>/dev/null      # park it before the stream even starts
  cd "$TMP"; watch_start
  watch_wait_lines 3 "$WATCH_TIMEOUT" || fail "sync of the already-parked lane never landed"
  [ "$(watch_kind 2)" = sync ]
  [[ "$(watch_line 2)" == *'"state":"parked"'* ]] || fail "the baseline wasn't parked: $(watch_line 2)"

  wt_run comeback
  [ "$status" -eq 0 ] || fail "resume itself failed: $output"

  watch_wait_lines 4 "$WATCH_TIMEOUT" || fail "the resume never reached the stream: $(cat "$WATCH_OUT")"
  [ "$(watch_kind 4)" = resumed ] || fail "line 4 wasn't resumed: $(watch_line 4)"
  [[ "$(watch_line 4)" == *'"state":"live"'* ]]
}

@test "watch: a landed branch swept by reap appears as reaped" {
  local main dir; main="$(mkrepo alpha)"; dir="$(mkwt "$main" done)"
  cd "$TMP"; watch_start
  watch_wait_lines 3 "$WATCH_TIMEOUT" || fail "sync of the pre-existing lane never landed"

  git -C "$main" merge -q --no-edit worktree-done
  wt_run reap
  [ "$status" -eq 0 ] || fail "reap itself failed: $output"

  watch_wait_lines 4 "$WATCH_TIMEOUT" || fail "the reap never reached the stream: $(cat "$WATCH_OUT")"
  [ "$(watch_kind 4)" = reaped ] || fail "line 4 wasn't reaped: $(watch_line 4)"
  [[ "$(watch_line 4)" == *'"name":"done"'* ]]
}

@test "watch: stdout is NDJSON only — every line stands alone as one JSON object" {
  local main; main="$(mkrepo alpha)"; mkwt "$main" clean >/dev/null
  cd "$TMP"; watch_start
  watch_wait_lines 3 "$WATCH_TIMEOUT" || fail "stream never settled"
  while IFS= read -r line; do
    [[ "$line" == \{*\} ]] || fail "a stdout line wasn't a bare JSON object: $line"
  done <"$WATCH_OUT"
}

# ── the rename (docs/rename.md §3) ───────────────────────────────────────────
#
# One binary, two names, until 1.1.0. These two tests are the black-box half of
# the bilingual release: they prove the old spelling still works from OUTSIDE
# the process, which is where every consumer that matters lives.

@test "rename: the binary answers to both names, identically" {
  local dir; dir="$(dirname "$WT")"
  [ -e "$dir/scruff" ] || skip "no scruff binary beside $WT — run make build"

  run "$dir/scruff" --version
  [ "$status" -eq 0 ]
  local new="$output"

  run "$dir/holt" --version
  [ "$status" -eq 0 ]
  [ "$output" = "$new" ]
}

# The deprecation notice is for a HUMAN and nobody else. Every non-interactive
# caller of this binary is one that would be hurt by a stray line on stderr:
# Claude Code's WorktreeCreate/Remove hooks, the bar plugins polling several
# times a minute, and anything parsing --json. stdout is a pipe here, so this
# asserts the gate holds.
@test "rename: invoking the old name is silent when stderr is not a terminal" {
  local dir; dir="$(dirname "$WT")"
  [ -e "$dir/holt" ] || skip "no holt symlink beside $WT — run make build"

  run "$dir/holt" --version
  [ "$status" -eq 0 ]
  [[ "$output" != *"now \`scruff\`"* ]]
}
