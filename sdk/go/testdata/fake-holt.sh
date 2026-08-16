#!/usr/bin/env bash
# A stand-in for the real `holt` binary, used only by this SDK's own tests.
# Emits fixed --json / watch --json payloads so the SDK's parsing, process
# lifecycle and error mapping can be exercised without a Go build.
set -euo pipefail

case "${1:-}" in
  --json|list)
    cat <<'JSON'
{
  "holt": "0.1.0-dev",
  "schema": 1,
  "lanes": [
    {
      "name": "sparkle",
      "repo": "hausfold/haus",
      "main": "/repo/haus",
      "branch": "worktree-sparkle",
      "path": "/repo/.holt/haus/sparkle",
      "parent": "/repo/haus",
      "agent": "claude",
      "state": "live",
      "occupied": true,
      "dirty": false,
      "landed": { "verdict": "no", "via": null, "confidence": "certain" },
      "post_merge_ahead": { "commits": 0, "pr": 0 },
      "last_commit": "spec: SDK smoke test fixture"
    },
    {
      "name": "frost",
      "repo": "hausfold/haus",
      "main": "/repo/haus",
      "branch": "worktree-frost",
      "path": "/repo/.holt/haus/frost",
      "parent": "/repo/haus",
      "agent": "codex",
      "state": "parked",
      "occupied": null,
      "dirty": null,
      "landed": { "verdict": "contained", "via": "merge-tree-empty", "confidence": "advisory" },
      "post_merge_ahead": { "commits": 0, "pr": 0 },
      "last_commit": "park: set aside"
    }
  ],
  "warnings": []
}
JSON
    ;;

  watch)
    echo '{"kind":"hello","seq":0,"holt":"0.1.0-dev","schema":1,"capabilities":["registry"]}'
    echo '{"kind":"sync","seq":1,"ts":"2026-08-07T02:11:04Z","source":"registry","lane":{"name":"sparkle","repo":"hausfold/haus","main":"/repo/haus","branch":"worktree-sparkle","path":"/repo/.holt/haus/sparkle","parent":"/repo/haus","agent":"claude","state":"live","occupied":true,"dirty":false,"landed":{"verdict":"no","via":null,"confidence":"certain"},"post_merge_ahead":{"commits":0,"pr":0},"last_commit":"c1"}}'
    echo '{"kind":"ready","seq":2,"ts":"2026-08-07T02:11:04Z"}'
    sleep 0.05
    echo '{"kind":"created","seq":3,"ts":"2026-08-07T02:11:05Z","source":"registry","lane":{"name":"fresh","repo":"hausfold/haus","main":"/repo/haus","branch":"worktree-fresh","path":"/repo/.holt/haus/fresh","parent":"/repo/haus","agent":"claude","state":"live","occupied":true,"dirty":true,"landed":{"verdict":"no","via":null,"confidence":"certain"},"post_merge_ahead":{"commits":0,"pr":0},"last_commit":"c2"}}'
    # Stay alive until killed, same as the real `watch`.
    trap 'exit 0' TERM INT
    while true; do sleep 0.05; done
    ;;

  child)
    echo "/repo/.holt/other/new-lane"
    ;;

  park)
    exit 0
    ;;

  heartbeat)
    if [[ " $* " == *" --release "* ]]; then
      echo "released $2" >&2
    fi
    exit 0
    ;;

  reap-refused)
    echo "refused: occupied" >&2
    exit 2
    ;;

  resume)
    # Mirrors the real resume's non-TTY behaviour: print the reopen command
    # instead of exec'ing anything, since stdout is a pipe under test.
    echo "checkout ready. Reopen the claude chat with:"
    echo "    cd /repo/.holt/haus/sparkle && claude --resume"
    ;;

  *)
    echo "fake-holt: unhandled args: $*" >&2
    exit 1
    ;;
esac
