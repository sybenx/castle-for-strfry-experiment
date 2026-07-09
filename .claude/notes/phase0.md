# Phase 0 notes — for Phase 1 (and beyond)

Things a future session needs that aren't obvious from just reading the
skeleton. Not a substitute for CLAUDE.md/PLAN.md/DECISIONS.md — this is
handoff trivia.

## Module path
`github.com/sybenx/castle-for-strfry-experiment` (taken from the `origin`
remote). CLAUDE.md's repo-layout example says `castle/` — that's just the
illustrative dir name in the spec, not the actual module path. Don't
"correct" it to match.

## internal/stateformat already exists — use it, don't reinvent it
`Banned{Pubkeys []string}` and `Citizens{Pubkeys []string}` are already
defined in `internal/stateformat/stateformat.go`. Phase 1's gatekeeper
reader and Phase 3a's steward writer must both import this package rather
than declaring their own structs — that's the whole point of building it
in Phase 0 (see DECISIONS.md). If the on-disk shape needs to change, change
it here, once.

## gatekeeper/main.go is a real stdin/stdout loop already, not a blank file
The Phase 0 stub already speaks strfry's plugin protocol correctly
(`pluginRequest`/`pluginEvent`/`pluginResponse`, JSONL in/out, per-line
flush, malformed-line survival, stderr for diagnostics). It just always
accepts. Phase 1 should **extend this loop** (hashset checks, Castle Mail
rule, token bucket, hot-reload) rather than starting over — the protocol
plumbing, buffered scanner sizing, and flush behavior are already right.
`sourceInfo` is parsed into `pluginRequest.Source` but currently unused;
that's the per-IP bucket's input in Phase 1.

`gatekeeper/testdata/` exists but is empty (just `.gitkeep`). Fixtures
(citizens.json, banned.json, event lines) go there in Phase 1.

## make smoke is unverified — no Docker on this machine
`deploy/smoke.sh` currently only boots a scratch strfry via
`deploy/docker-compose.yml` and polls it with `nak req` until it answers —
no fixture publishing, no assertions yet (that's explicitly Phase 1/3a
work per PLAN.md). It has never actually been run.

**Specifically unverified: the strfry image tag.** `docker-compose.yml`
uses `dockurr/strfry:latest` — picked without confirming it's the image
you want to standardize on. Check/replace this in Phase 1 before wiring
real fixture assertions into the smoke script, or the whole thing fails at
the first `docker compose up`.

## CI's static-binary check only covers linux/amd64
The GitHub Actions runner is amd64, so `ldd` can only meaningfully check
the amd64 gatekeeper binary (cross-arch `ldd` doesn't work). The arm64
build is still compiled and `file`-checked, just not `ldd`-checked. This is
fine (`CGO_ENABLED=0` statically links both), just noting why the workflow
only has one `ldd` call.

## bytecheck exists but isn't in CI yet — intentional
`make bytecheck` works today (verified: passes on the placeholder, fails
correctly when the file is missing). It's deliberately not called from
`.github/workflows/ci.yml` until Phase 6a, per DECISIONS.md ("bytecheck is
strict from day one; phasing lives in CI wiring, not Makefile logic"). Add
the CI step in Phase 6a, don't touch the Makefile target.

## steward's env config is a stub, hardcoded, not fully wired
`steward/main.go`'s `loadConfig()` only actually reads `OWNER_PUBKEY`,
`STRFRY_CONTAINER` (unused so far), `RAID_CRON`, `NIP05_DOMAIN`, and
`LISTEN` from the environment. `OuterTTLDays`, `CycleMinutes`, `RaidDryRun`,
`MaxInvites`, `MaxDepth` are hardcoded to their spec defaults, not parsed
from `OUTER_TTL_DAYS` / `CYCLE_MINUTES` / `RAID_DRY_RUN` / `MAX_INVITES` /
`MAX_DEPTH` env vars yet. Whoever wires the cycle loop (Phase 3a) needs to
finish this parsing — it's a gap, not a design choice.

## bin/ is gitignored
Build output goes to `bin/<os>-<arch>/{gatekeeper,steward}`, excluded via
`.gitignore`. `.env` is also gitignored.
