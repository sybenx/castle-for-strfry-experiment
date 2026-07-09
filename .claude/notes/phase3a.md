# Phase 3a notes — for Phase 3b (and beyond)

Handoff trivia for the cycle loop. Not a substitute for CLAUDE.md/PLAN.md/
DECISIONS.md.

## What landed

`steward/nostr.go`: `NostrFetcher` interface (`LatestKind3`, `Reports`,
`Reactions`, `Event`) plus `relayFetcher`, the real go-nostr-backed
implementation (`nostr.RelayConnect` + `QuerySync` per relay, 10s timeout
per call, individual relay errors logged and skipped rather than sinking
the whole fetch).

`steward/cycle.go`: `Cycle` struct (StateDir/Owner/OwnRelay/PublicRelays/
MaxInvites/MaxDepth/Fetcher/CLI/Now) and `Cycle.Run` — follows sync, report
intake (ledger source-id dedupe — the zombie-ban fix), react-warding
(watermark-driven, PUBLIC_RELAYS fallback for note resolution), ledger
append + atomic banned.json/citizens.json/tree.json writes, and
purge-newly-banned via the `strfryCLI` interface (`dockerStrfryCLI` shells
out to `docker exec <container> strfry delete --filter ... `, batched at
`deleteBatchSize=50`). `FollowsSnapshot` (follows.json) and the react
watermark (`react_watermark.json`, `{"since": <ts>}`) are the two
non-ledger durable state files this phase adds — see DECISIONS.md for why
they aren't ledger-derived.

`steward/main.go`: `loadConfig()` now actually parses every env var from
CLAUDE.md's Component 2 list (`PUBLIC_RELAYS` comma-split, ints via
`envInt`, bool via `envBool`, all fail-safe to spec defaults on a
malformed value). `main()` builds a real `Cycle` via `NewCycle` and runs it
once immediately, then on a `CYCLE_MINUTES` ticker, until SIGINT/SIGTERM
(via `signal.NotifyContext`). No HTTP server yet — that's Phase 5a; this
binary today is cycle-loop-only.

`steward/cycle_test.go`: fakes for `NostrFetcher` and `strfryCLI`, and
every Phase 3a checklist item from CLAUDE.md — zombie-ban regression (3
cycles, same old report, pubkey stays pardoned), new-report-after-pardon
re-bans, ban-domain convention (no purge call for domain bans — those
resolve at raid time), follows never shrink on fetch error, react-warding
via local relay AND via the PUBLIC_RELAYS fallback, react-warding skips
the Lord and skips (never demotes) an already-elevated pubkey, watermark
prevents reprocessing across cycles.

`gatekeeper/roundtrip_test.go`: the Phase 3a round-trip test PLAN.md asks
for. Since steward and gatekeeper are both `package main` and can't import
each other, it writes `banned.json`/`citizens.json` using the same
`internal/stateformat` types and the same atomic-write-then-rename steward
uses, then loads them through gatekeeper's real `store`/`refresh`/
`isBanned`/`isCitizen` — the actual parser, not a stand-in.

## `make smoke` is real now — read deploy/smoke.sh before touching it

It no longer uses `docker compose` (see DECISIONS.md for why) — it manages
a dedicated Docker network (`castle-smoke`), the `castle-state` volume, and
a `castle-smoke-strfry` container with plain `docker` commands, all torn
down in a trap (including a defensive cleanup-before-start in case a prior
run was interrupted). It pre-seeds the compiled `gatekeeper` binary into
`castle-state` *before* strfry's first start (a plugin binary that appears
after strfry has already spawned its writePolicy child wouldn't be picked
up). `deploy/smoke-conf/strfry.conf` is the full bootable config this
needs; `strfry.conf.patch` is still just the hand-merge fragment for real
deployments, untouched.

The script generates four real secp256k1 keypairs via `nak`, publishes a
kind-3 (follow), a kind-1984 (spam report), a kind-1 (a stranger's note),
and a kind-7 (the Lord reacting to that note) to the scratch strfry, runs
the real compiled `steward` binary for ~8s in a one-shot container on the
same network (long enough for one cycle against a local relay), stops it,
then reads back `banned.json`/`citizens.json`/`ledger.jsonl` from the
shared volume and asserts all three reflect the fixtures correctly.
Finally it republishes from the now-banned spammer key and confirms
gatekeeper rejects it with the exact themed message — tying Phase 1 and
Phase 3a together end to end. Verified passing in this environment
(colima, arm64) via both `./deploy/smoke.sh` directly and `make smoke`.

**Known gap, by design, not oversight:** the smoke test's one-shot steward
container has no docker CLI or socket, so the purge-newly-banned step's
`docker exec` fails there (logged, non-fatal). See DECISIONS.md — this is
deferred to Phase 4, where raid.go will need the real docker-exec delete
path exercised anyway.

## Still not done (Phase 3b and later)

- No stats.json yet — cycle.go's Run doesn't do step 6 (stats) at all;
  that's explicitly Phase 3b in PLAN.md's split.
- No kind-0 name cache.
- No update-check banner.
- steward has no HTTP server yet (Phase 5a) — `main()` is cycle-loop-only.
  `LISTEN` is parsed into config but unused so far.
