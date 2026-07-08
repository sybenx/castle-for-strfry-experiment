# PLAN.md — build plan for the Castle

Read CLAUDE.md first; it is the spec and the source of truth. This file is
the order of operations. Work one phase per session where possible; Phases
3, 5, and 6 are pre-split because they won't fit in one. Every phase ends with its
tests green and a commit. Do not start a phase until the previous phase's
acceptance criteria pass. Check off each phase here (`[x]`) when accepted.
Resist adding anything not in the spec — light as a whip.

## [ ] Phase 0 — Skeleton (30 min)
Repo layout per CLAUDE.md, Go module, Makefile (build/test/smoke/bytecheck,
cross-compile linux/amd64 + linux/arm64; bytecheck is strict from day one —
missing towncrier/index.html is a FAILURE, >60KB is a failure; it simply
isn't wired into CI until Phase 6a, so the guard has one behavior and can
never rot into a no-op), .env.example (RAID_DRY_RUN=true,
RAID_CRON empty), empty test files, seeded DECISIONS.md, CI workflow that
runs `make test` on push and asserts both gatekeeper binaries are static
(`ldd` says "not a dynamic executable").
**Accept:** `make build` produces static binaries for both arches; CI green
on a trivial test including the static check.

## [ ] Phase 1 — gatekeeper (the plugin)
Pure stdlib. stdin/stdout JSONL loop, hashset checks against banned.json /
citizens.json, Castle Mail recipient rule, per-IP token bucket with idle
eviction, mtime hot reload with injectable poll interval, fail-open on
missing files, malformed-line resilience, a native fuzz target on the stdin
loop, themed reject messages. Ephemeral kinds (20000–29999) from
non-citizens go through the same token bucket — no exemption (see
DECISIONS.md); pin this with a test. The banned.json/citizens.json format
types are born in a shared, stdlib-only internal package (e.g.
internal/stateformat) so Phase 3a's writers and gatekeeper's reader can
never drift — creating it now beats refactoring a tagged v0.1.0 later.
Committed fixtures in gatekeeper/testdata/. Unit tests for every gatekeeper
row of the CLAUDE.md checklist.
**Accept:** all gatekeeper checklist tests pass; fuzz target runs clean for
30s; manual smoke against an ad-hoc local strfry in docker accepts a citizen
event, rejects a banned one, and CONFIRMS strfry routes an ephemeral-kind
event through the write policy at all (the DECISIONS.md rate-limit call
assumes it; if strfry never invokes the plugin for ephemeral kinds, note
that in DECISIONS.md and drop the pinning test). Commit + tag v0.1.0.

## [ ] Phase 2 — steward core: ledger, tree, elevation (no network)
ledger.jsonl append/replay with all verbs (invite/remove/ennoble/ban/pardon/
ban-domain/pardon-domain/elevate/lower/flip-visibility/archive-run/raid-run);
every ledger line carries `"v":1` from the very first write (one field of
insurance: a future format change becomes a migration, not a replay break;
replay rejects unknown versions loudly);
tree.go with invite/remove/ennoble/ban-cuts-branch, MAX_INVITES/MAX_DEPTH;
elevation.go (one set + visibility flag, ban-beats-elevation, no
reparenting); eviction timestamps recorded on removal; citizens
recomputation ({Lord} ∪ tree ∪ follows ∪ elevated); atomic state-file
writers (temp in same dir + rename). Pure logic — test exhaustively now.
Property test: ledger replay always reconstructs identical tree + elevation
+ ban state.
**Accept:** all tree + elevation + ledger checklist tests pass, including
"wards absent from every public projection."

## [ ] Phase 3a — steward cycle: sync + intake
go-nostr client work: follows sync with follows.json last-good snapshot
(never shrink on error, survive restart, only replace with newer kind 3);
kind-1984 report intake (spam/illegal/malware pubkey bans, `ban-domain:`
lines; NO kind-5 voids, NO pardon-list sync); react-warding from kind 7
with watermark; ledger merge; purge-on-ban via the docker-exec strfry
wrapper (interfaced so tests fake it).
**Accept:** steward checklist tests for sync/intake/react-warding pass;
cycle runs against a scratch strfry in docker-compose with fixture events
published via nak; banned.json/citizens.json/tree.json come out correct;
a round-trip test loads steward-written banned.json/citizens.json through
gatekeeper's actual parser via the shared internal/stateformat package
created in Phase 1 (stdlib-only, so gatekeeper's constraint holds; this
kills fixture/writer drift between Phase 1's hand-written fixtures and the
real writers).

## [ ] Phase 3b — stats, name cache, update check
stats.json per the schema (public counts exclude wards; raids.next null when
manual), batched `strfry scan --count`, daily GitHub release check feeding
`version` in stats.json. Kind-0 name/avatar cache for tree members (fetch
from local relay first, PUBLIC_RELAYS as fallback; atomic cache file; lazy
refresh with a staleness threshold; tree members only — never wards). Phase
5a's /api/tree only READS this cache; the network code lives here.
**Accept:** stats.json validates against the schema from a live compose
stack; ward count appears nowhere; name cache populates for tree members
and contains no ward pubkeys.

## [ ] Phase 4 — the raid
Domain re-enumeration + local kind-0 nip05 sweep at raid time (interface
the `/.well-known/nostr.json` fetcher exactly like the strfry-exec wrapper
so raid tests fake it — no live HTTP in tests); streaming
scan-then-delete with the three keep-conditions (citizen, Castle Mail,
eviction grace); batching; RAID_DRY_RUN honored (default ON); ledger logging
of purge counts; optional RAID_CRON scheduling + the manual trigger hook the
API will call.
**Accept:** raid checklist tests pass, including "dry run deletes nothing,"
"stranger-to-stranger gift wrap past TTL deleted, citizen-addressed gift
wrap survives at any age," and "evicted member's notes survive the grace
window, die after."

## [ ] Phase 5a — HTTP API
NIP-98 verification (sig, u, method, ±60s, 5-min replay guard), all
endpoints from CLAUDE.md including /api/wards (Lord only), /api/elevate,
/api/lower, /api/archive, /api/raid; immediate state rewrite on mutation;
per-IP rate limit; same-origin CORS; static file serving for towncrier;
NIP-05 serving when NIP05_DOMAIN is set.
**Accept:** API checklist tests pass; curl + nak-signed headers can invite,
remove, elevate, ban, pardon, and trigger a dry-run raid end-to-end against
the compose stack; /api/wards refuses a non-Lord signature.

## [ ] Phase 5b — the scribe
POST /api/archive one-shot job: paginated REQ backfill (until-cursor,
limit 500, polite pacing, backoff on CLOSED), replay into the castle over a
local websocket, one job at a time, own goroutine/failure domain, counts
logged to ledger.
**Accept:** scribe checklist tests pass against a fixture relay; killing the
scribe mid-job leaves the cycle untouched.

## [ ] Phase 6a — towncrier: the public page
One index.html, < 60KB (the always-strict bytecheck gets wired into the CI
workflow in this phase), no deps, no build. Public sections per CLAUDE.md: Lord, Court (tree as
nested <details> with stars), Favored, Citizenry, Vault, Evicted
(struck-through + expiry, "until the next raid" when manual), Wild West
("at the Lord's pleasure" when manual), Exiled, NIP-11 footer,
copy-relay-URL, njump profile links. Read-only — no sign-in yet; leave a
placeholder "Enter the castle" button that explains NIP-07.
**Accept:** renders correctly from steward with real stats.json and
/api/tree; no ward data appears anywhere in the page or the responses it
fetches (grep the served payloads in a test, not by eye); payload under
budget in CI.

## [ ] Phase 6b — towncrier: the NIP-07 layer
Sign-in, member invite/remove with branch-fall confirm, full Lord controls
including inline star toggles, ban/pardon with domain field, archive
buttons, raid-now, update banner, double-confirm on banning the elevated,
and the Lord-only Wards section fed exclusively by authenticated
/api/wards (add npub, lower, visibility flip, react-ward sources shown).
**Accept:** a NIP-07 extension performs an invite and a ward-add in a
browser; a non-Lord sign-in shows no ward UI and no ward data appears in
any response it can trigger; still under the byte budget in CI.

## [ ] Phase 7 — distribution
Release workflow (binaries + checksums + ghcr multi-arch image), install.sh
(print-don't-edit for strfry.conf / compose / proxy), uninstall.sh, README
with screenshot and the docker.sock disclosure. Test install.sh against a
clean container running a stock strfry compose stack.
**Accept:** following install.sh's printed instructions on a fresh box
yields a working castle with manual dry-run raids; with the printed proxy
config applied, the smoke test verifies the real-IP header reaches
gatekeeper (two client IPs get independent rate buckets — per CLAUDE.md,
without this the limiter is a no-op or a self-DoS); uninstall leaves the
stock setup intact.

## Standing orders
- After any change to tier or elevation logic, re-run the full gatekeeper
  fixture suite AND the elevation privacy tests.
- Never write to stdout in gatekeeper except protocol responses.
- Never let a network failure shrink citizens or forget bans (ledger +
  follows.json are truth).
- Wards in public output = release-blocking bug. Grep public projections in
  tests, not by eye.
- Keep towncrier's byte budget: `wc -c` in CI, fail over 60KB.
- steward state is pubkeys, timestamps, admin actions — never event ids. Any
  feature that wants a stored event id gets written to DECISIONS.md as
  rejected, not implemented.
- When a decision isn't covered by CLAUDE.md, choose the option with less
  code, and note it in DECISIONS.md.
