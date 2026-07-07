# PLAN.md — build plan for the Castle

Read CLAUDE.md first; it is the spec and the source of truth. This file is the
order of operations. Work one phase per session where possible. Every phase
ends with its tests green and a commit. Do not start a phase until the
previous phase's acceptance criteria pass. Resist adding anything not in the
spec — light as a whip.

## Phase 0 — Skeleton (30 min)
Repo layout per CLAUDE.md, Go module, Makefile (build/test/cross-compile
targets for linux/amd64 + linux/arm64), .env.example, empty test files, CI
workflow that runs `make test` on push.
**Accept:** `make build` produces both static binaries for both arches;
CI green on a trivial test.

## Phase 1 — gatekeeper (the plugin)
Pure stdlib. stdin/stdout JSONL loop, hashset checks against banned.json /
citizens.json, Castle Mail recipient rule, per-IP token bucket, mtime-based
hot reload, fail-open on missing files, malformed-line resilience, themed
reject messages. Unit tests with piped fixtures for every row of the tier
table in CLAUDE.md.
**Accept:** all gatekeeper tests in the CLAUDE.md checklist pass; binary is
static (`ldd` says not dynamic); a manual smoke against a local strfry in
docker accepts a citizen event and rejects a banned one. Commit + tag v0.1.0.

## Phase 2 — steward core: ledger + tree (no network yet)
ledger.jsonl append/replay; tree.go with invite/remove/ennoble/ban-cuts-branch,
MAX_INVITES/MAX_DEPTH enforcement; state-file writers (atomic temp+rename);
citizens recomputation. This is pure logic — test it exhaustively now, before
networking exists. Property test: ledger replay always reconstructs identical
state.
**Accept:** all tree + ledger tests in the checklist pass.

## Phase 3 — steward sync: the cycle
go-nostr client work: follows sync (never shrink on error), report intake
(spam/illegal/malware only), kind-5 voids, pardon list, domain well-known
enumeration + local kind-0 scan, purge-on-ban via docker-exec strfry CLI
wrapper (interface it so tests can fake it), promotion walker, stats.json.
**Accept:** cycle runs against a scratch strfry in docker-compose with
fixture events published via nak; state files and stats.json come out correct.

## Phase 4 — the raid
Streaming scan-then-delete with all four keep-conditions, batching, dry-run
mode (default ON), ledger logging of purge counts, RAID_CRON scheduling +
manual trigger hook for the API.
**Accept:** raid tests in the checklist pass, including "dry run deletes
nothing" and "ex-citizen events deleted after branch cut."

## Phase 5 — HTTP API
NIP-98 verification (sig, u, method, ±60s, replay guard), the six endpoints +
POST /api/raid, immediate state rewrite on mutation, per-IP rate limit,
same-origin CORS, static file serving for towncrier.
**Accept:** API tests in the checklist pass; curl + nak-signed headers can
invite, remove, ban, pardon end-to-end against the compose stack.

## Phase 6 — towncrier
One index.html, < 60KB, no deps, no build. Public stats sections, the tree as
nested <details>, raid countdown, NIP-11 footer, copy-relay-URL. Then the
NIP-07 layer: sign-in button, invite/remove for members, full controls for
the Lord, branch-fall confirm dialog, graceful no-extension message.
**Accept:** renders correctly from steward with real stats.json; a NIP-07
extension can perform an invite in a browser; total payload under budget.

## Phase 7 — distribution
Per the Distribution section of CLAUDE.md: release workflow (binaries +
ghcr multi-arch image + checksums), install.sh, uninstall.sh, README with
screenshot. Test install.sh against a clean VM/container running a stock
strfry compose stack.
**Accept:** `curl | bash` on a fresh box yields a working castle with
dry-run raids; uninstall restores the original strfry.conf.

## Phase 8 — NIP-86 shim (optional, last)
Feature-flagged; routes by Content-Type on the same port; OWNER_PUBKEY only;
writes through the ledger.
**Accept:** a NIP-86 admin client can list/ban/allow.

## Standing orders
- After any change to tier logic, re-run the full gatekeeper fixture suite.
- Never write to stdout in gatekeeper except protocol responses.
- Never let a network failure shrink citizens or forget bans (ledger is truth).
- Keep towncrier's byte budget: check `wc -c` in CI, fail over 60KB.
- When a decision isn't covered by CLAUDE.md, choose the option with less
  code, and note it in a DECISIONS.md.
