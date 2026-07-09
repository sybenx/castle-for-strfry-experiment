# Phase 1 notes — for Phase 2 (and beyond)

Handoff trivia for gatekeeper. Not a substitute for CLAUDE.md/PLAN.md/
DECISIONS.md.

## What landed

`gatekeeper/main.go` is now the real thing: tier decision (banned → Castle
Mail → citizens → Outer Lands, first match wins, matching CLAUDE.md's
table), hot-reloading `store` for banned.json/citizens.json (mtime-gated,
poll interval injectable, fail-open on missing files), a per-IP token
`limiter` (30/min, burst 60, idle eviction swept at most once/minute), and
`processLine`/`decide` factored out of `main()` so tests and the fuzzer can
drive them directly without a subprocess.

`gatekeeper/testdata/` has committed fixtures: `citizens.json`,
`banned.json`, `events.jsonl` (nine plugin-request lines covering every
tier-table row plus two deliberately malformed lines). Test pubkeys are
recognizable repeated-hex-digit strings (`1111...`=tree citizen,
`2222...`=follow citizen, `3333...`=ward, `9999...`=banned, `aaaa...`=gift
wrap author→citizen, `bbbb...`=zap author→citizen, `cccc...`=gift wrap
author→stranger, `5555...`=stranger) — none of these are real secp256k1
keypairs, they're just distinct strings gatekeeper's string-set matching
doesn't care about the difference. Don't confuse them with the *real*
generated keys used in the manual smoke check below.

`gatekeeper/fuzz_test.go` has `FuzzProcessLine`; ran clean for 30s
(6.7M execs, zero crashes) — see the DECISIONS.md-adjacent note below,
this was run in this environment even though Docker wasn't available.

## No Docker in this environment — same gap as Phase 0

Still true. This blocked the two things PLAN.md's Phase 1 acceptance
criteria ask for that specifically require a real strfry container:

1. "manual smoke against an ad-hoc local strfry in docker accepts a citizen
   event, rejects a banned one" — **partially covered**, see below.
2. "CONFIRMS strfry routes an ephemeral-kind event through the write policy
   at all" — **not verified**. DECISIONS.md's ephemeral-kind entry now
   says this explicitly; the gatekeeper-side behavior (ephemeral events
   from non-citizens ride the bucket) is tested and correct on gatekeeper's
   side regardless, but whether strfry actually calls the plugin for kind
   20000-29999 at all is still an open empirical question. **Whoever gets
   Docker next: run `make smoke` against a real strfry, publish an
   ephemeral-kind event as a banned pubkey via nak, and check whether
   gatekeeper's reject message comes back in the relay's OK response. If
   strfry never calls the plugin for ephemeral kinds, PLAN.md says to drop
   `TestDecide_EphemeralStrangerRidesBucket`'s premise note and record it
   in DECISIONS.md — the test itself still documents intended gatekeeper
   behavior even if strfry-side wiring turns out not to exercise it.**

`nak` *is* installed locally (`/opt/homebrew/bin/nak`, v0.19.9), even
though Docker isn't. That made a real (non-Docker) verification possible:
built the actual compiled gatekeeper binary, generated genuine secp256k1
keypairs and NIP-01-signed events with `nak key generate` / `nak event
--sec ...`, and fed them through the plugin-protocol stdin/stdout format
directly into the binary (no strfry in the loop, but real signed events
and the real binary, not just Go unit tests). Confirmed by hand:
- citizen-authored note → accept
- banned-authored note → reject with the exact themed message
- gift wrap (kind 1059) from an unrelated random key, p-tagging a citizen
  → accept
- 65 identical stranger events from one source IP → 60 accepted, 5
  rejected with the rate-limit message (exactly the spec'd burst=60)
- hot-reload against the *real* filesystem clock and the *real* default
  1-second poll interval (not the fake-clock unit test): banned a
  previously-accepted pubkey mid-process by rewriting banned.json, waited
  >1s, same pubkey's next event was rejected — on a long-lived running
  process, not a fresh one.

This was throwaway, done in the scratchpad dir, not committed. If you want
to redo it, the recipe is: `nak key generate -q` for a hex seckey, `nak key
public <sec> -q` for the hex pubkey, `nak event --sec <sec> -k <kind> -c
<content> [-p <pubkey>] -q` prints the signed event JSON without
publishing (no relay arg = print only), wrap it as
`{"type":"new","event":<that>,"sourceInfo":"<ip>"}` and pipe lines into the
built binary with `CASTLE_STATE_DIR` — wait, no: **gatekeeper has no env
var for its state dir**, it's hardcoded to `/plugin` (see DECISIONS.md).
For a from-binary manual test like this you have to either symlink/copy a
`/plugin` you can write to (needs root or a mount namespace trick) or
temporarily patch the `stateDir` const — the smoke check above briefly
edited the constant locally, tested, then reverted; it is not left in the
tree. `go test ./gatekeeper/...` and the fuzz target don't need this at
all since they construct `*store` directly against `t.TempDir()`.

## gatekeeper has zero env vars, on purpose

CLAUDE.md's env-config list is steward-only. Both the state directory
(`/plugin`, hardcoded) and the rate-limit numbers (30/min, burst 60,
10-minute idle eviction — all named as defaults in CLAUDE.md prose, not
as an env-config entry) are compile-time constants in `gatekeeper/main.go`.
If a future phase needs to change these, prefer editing the constants over
adding a getenv branch, unless CLAUDE.md itself grows a gatekeeper env-var
list — see DECISIONS.md's two new entries for the reasoning.

## deploy/smoke.sh is untouched

Still the Phase 0 placeholder (boots scratch strfry, polls until it
answers, no assertions). Phase 1's acceptance bar technically wants real
assertions here too, but wiring `make smoke` to actually build gatekeeper
into the castle-state volume, patch strfry.conf, and drive nak-signed
accept/reject/ephemeral-kind checks against a live strfry needs Docker to
write and verify correctly — doing it blind risked landing a subtly broken
script that *looks* done. Left for whoever has Docker; the manual
verification above (real binary, real signed events, no strfry) is the
best that could be responsibly done here. Phase 3a's PLAN.md entry also
expects a docker-compose-based cycle test, so this gap will need closing
one way or another by then regardless.

## internal/stateformat was untouched

Phase 0's `Banned`/`Citizens` structs were already exactly what gatekeeper
needed; no changes. gatekeeper is now the second (and last, per
DECISIONS.md's "one wrapper" spirit for stateformat) real consumer —
steward's writer side is Phase 3a.
