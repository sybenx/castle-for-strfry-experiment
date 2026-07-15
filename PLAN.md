# PLAN.md — build plan for the Castle

Read CLAUDE.md first; it is the spec and the source of truth. This file is
the order of operations. Work one phase per session where possible. Every
phase ends with its tests green and a commit. Do not start a phase until
the previous phase's acceptance criteria pass. Check off each phase here
(`[x]`) when accepted. Resist adding anything not in the spec — light as a
whip.

**History note:** the project was re-scoped mid-build (after Phase 3a).
The write-policy plugin (gatekeeper), bans/report intake, Castle Mail,
rate buckets, react-warding, domain bans, the scribe, byte
accounting/gates, and NIP-05 serving were all cut — see DECISIONS.md,
which records each with its revert path. Phase numbers below keep their
original identities so old commits and notes still make sense; Phase 1 is
struck rather than renumbered away.

## [x] Phase 0 — Skeleton
Repo layout, Go module, Makefile (build/test/smoke/bytecheck, cross-compile
linux/amd64 + linux/arm64; bytecheck is strict from day one — missing
towncrier/index.html is a FAILURE, >60KB is a failure; wired into CI in
Phase 6a), .env.example, CI workflow running `make test` and asserting the
binaries are static.

## [x] Phase 1 — gatekeeper *(built, then removed in Phase D)*
Shipped as v0.1.0/v0.1.1; removed at re-scope. Lives in git history if
write-path policy ever returns.

## [x] Phase 2 — steward core: ledger, tree, elevation (no network)
ledger.jsonl append/replay, `"v":1` on every line, tree.go with
invite/remove/ennoble + MAX_INVITES/MAX_DEPTH, elevation.go, eviction
timestamps, citizens recomputation, atomic state-file writers, the replay
property test.

## [x] Phase 3a — steward cycle: follows sync
go-nostr client work: follows sync with follows.json last-good snapshot
(never shrink on error, survive restart, only replace with newer kind 3);
ledger merge; atomic state writes.
*(As built, this phase also included report intake, react-warding, and
purge-on-ban — all removed in Phase D.)*

## [x] Phase D — the demolition (do this NOW, before finishing 3b)
Remove everything the re-scope cut. One session, mostly deletion:
- Delete `gatekeeper/` (source, testdata, fuzz target), its Makefile
  targets, and its CI static-binary assertion (keep steward's). Fold
  `internal/stateformat` into the steward package if nothing else imports
  it — or leave it if folding causes churn; less code wins.
- Delete from steward: report intake (kind-1984 fetch, source-id dedupe,
  zombie-ban regression machinery), react-warding (kind-7 fetch, watermark,
  PUBLIC_RELAYS note lookup), purge-on-ban (the wrapper's second call
  site), ban/pardon/ban-domain/pardon-domain ledger verbs and their tree
  interactions (VerbBan branch-cut, `State.Citizens` banned-exclusion),
  `banned.json` writer, all mail/lands bucket references.
- Ledger replay now rejects the removed verbs loudly (unknown verb =
  loud failure, same rule as unknown version). Dev-only ledgers with old
  verbs are deleted, not migrated.
- `.env.example`: drop MAIL_RATE_PER_MIN, LANDS_RATE_PER_MIN,
  NIP05_DOMAIN. Keep the CLAUDE.md env list exactly.
- deploy/: drop the strfry.conf patch and the /plugin volume from the
  compose fragment; steward keeps state volume + docker.sock only.
- Delete every test pinning removed behavior; re-run the FULL remaining
  suite.
**Accept:** `make build` produces static steward only; `make test` green;
`grep -ri "gatekeeper\|banned\|1984\|gift wrap\|bucket" steward/` returns
only DECISIONS/CLAUDE references, no live code. Commit "the demolition",
tag v0.2.0.

## [x] Phase 3b — stats, name cache, update check
stats.json per the CLAUDE.md schema (public counts exclude wards;
raids.next null when manual), batched `strfry scan --count`, daily GitHub
release check feeding `version` in stats.json. Kind-0 name/avatar cache
for tree members, public favorites, and evicted members inside their grace
window (local relay first, PUBLIC_RELAYS fallback; atomic cache file; lazy
refresh with staleness threshold; never wards). Phase 5's /api/tree only
READS this cache; the network code lives here.
**Accept:** stats.json validates against the schema from a live compose
stack; ward count appears nowhere; name cache populates for tree members
and public favorites and contains no ward pubkeys.

## [x] Phase 4 — the raid
Streaming scan-then-delete with the two keep-conditions (citizen, eviction
grace); the per-raid ttl_days override with clamp (≥1, else 400) and
grace-decoupling (grace ALWAYS uses OUTER_TTL_DAYS); dry-run-as-preview
returning `{events}`; batching; RAID_DRY_RUN honored (default ON); ledger
raid-run line records purge count + ttl used + override-or-default;
optional RAID_CRON scheduling (always default TTL) + the manual trigger
hook the API will call. All deletes through the single strfry-CLI wrapper —
raid.go is the ONLY call site in the codebase.
**Accept:** raid checklist rows in CLAUDE.md all pass, including "dry run
deletes nothing and returns nonzero events," "evicted member survives the
grace window, dies after," "grace survives a smaller override," "override
respected / absent uses default / 0 rejected / ledger records ttl."

## [x] Phase 5 — HTTP API
NIP-98 verification (sig, u, method, ±60s, 5-min replay guard), all
endpoints from CLAUDE.md including /api/wards (Lord only), /api/elevate,
/api/lower, /api/raid (with ttl_days + dry_run body); immediate state
rewrite on mutation; per-IP rate limit; same-origin CORS; static file
serving for towncrier (placeholder index.html until Phase 6a).
**Accept:** API checklist tests pass; curl + nak-signed headers can
invite, remove, ennoble, elevate, lower, and trigger a dry-run raid
end-to-end against the compose stack; /api/wards refuses a non-Lord
signature.

## [x] Phase 6a — towncrier: the public page
One index.html, < 60KB (bytecheck wired into CI this phase), no deps, no
build. Public sections per CLAUDE.md: Lord, Court (nested <details> with
stars), Favored, Citizenry, Evicted (struck-through + expiry, "until the
next raid" when manual), Outer Lands (count, oldest age, days since last
raid, "at the Lord's pleasure" when manual), the ephemeral-DMs note,
NIP-11 footer, copy-relay-URL, njump profile links. Read-only — a
placeholder "Enter the castle" button that explains NIP-07.
**Accept:** renders correctly from steward with real stats.json and
/api/tree; no ward data appears anywhere in the page or the responses it
fetches (grep the served payloads in a test, not by eye); payload under
budget in CI.

## [x] Phase 6b — towncrier: the NIP-07 layer
Sign-in, member invite/remove with branch-fall confirm, full Lord controls
including inline star toggles, ennoble, the raid control (days input
pre-filled with OUTER_TTL_DAYS → Preview → confirm dialog stating "purge
stranger events older than N days — N events" → raid), update banner, and
the Lord-only Wards section fed exclusively by authenticated /api/wards
(add npub, lower, visibility flip).
**Accept:** a NIP-07 extension performs an invite and a ward-add in a
browser; a non-Lord sign-in shows no ward UI and no ward data appears in
any response it can trigger; still under the byte budget in CI.

## [x] Phase 7 — distribution
Release workflow (binary + checksums + ghcr multi-arch image), install.sh
(print-don't-edit for compose / proxy configs; no strfry.conf involvement
at all now), uninstall.sh, README with screenshot, the docker.sock
disclosure, and the free-writePolicy-slot note for spam plugins. Test
install.sh against a clean container running a stock strfry compose stack.
**Accept:** following install.sh's printed instructions on a fresh box
yields a working castle with manual dry-run raids; uninstall leaves the
stock setup intact. Tagged v0.4.0: release workflow verified live (binaries
+ checksums attached to the GitHub Release, multi-arch manifest confirmed
on ghcr.io), and the published image smoke-tested against a scratch strfry
container — `docker exec` from inside the image worked, stats.json's
`version.running` matched the tag.

## [x] Phase 8 — version-banner correctness (move comparison into steward)
*(Shipped as `version.status` — a four-value derived signal rather than a
bool; see DECISIONS.md, "`version.status` is steward's derived update
signal".)*

Code review of the working-tree towncrier version-banner change (see
DECISIONS.md for the finding) found the client-side `versionIsNewer` +
exact-tag-regex approach misclassifies real, reachable build strings.
Move the decision server-side per CLAUDE.md's "Version comparison is
steward's job, not towncrier's":

- In steward (stats.go), parse `c.RunningVersion` and the GitHub tag the
  same way — tolerate an optional `v`, ignore `git describe`'s
  `-N-gHASH`/`-dirty` suffix, compare numeric cores — and add a derived
  signal to `VersionInfo` (e.g. `UpdateAvailable bool`) instead of leaving
  towncrier to infer direction from raw strings. Cover it with a Go table
  test: exact match, dirty/describe suffix behind latest, dirty/describe
  suffix ahead of latest (already-newer local build), `dev`, bare-hash
  fallback, unprefixed tags on either side, unparseable `latest`
  (non-semver GitHub tag) — each asserting the correct banner state.
- towncrier only renders the signal steward computed; delete the
  client-side `versionIsNewer` helper and the exact-tag regex gate.
- The banner text must contain the actual one-line update command (the
  install.sh curl-pipe line from README.md), not just point at the
  README — CLAUDE.md's update-banner sentence requires the command
  itself.
- Once the design is settled, record it in DECISIONS.md (the "preview
  build, ahead of latest" banner state is a decision not covered by
  CLAUDE.md's original spec and needs an entry there, per the standing
  order below).

**Accept:** a Go test suite drives `checkRelease`/version-comparison
through the cases above and asserts `stats.json`'s update signal is
correct in each; towncrier has no version-parsing regex left; `make
bytecheck` still passes.

## [x] Phase 9 — the Census
Public DB-transparency layer per CLAUDE.md "The Census" and DECISIONS.md
"The Census re-scope": kind-aware ScanAll (raid's ScanUntil unchanged),
per-author/per-kind/encrypted aggregation inside the existing cycle scan,
census.json (derived view, atomic write), public GET /api/census, and
towncrier's census section (capped top/stale tables, totals, kind +
encrypted counts, full-JSON download link) plus the live event viewer
(client-side NIP-01 REQ, raw escaped text, 50-event cap).
**Accept:** Go tests cover the aggregation (counts, first/last seen, sort
orders, caps, ephemeral-kind exclusion) and the ward-indistinguishability
property (encrypted split + stale_outer classify a ward as outer; author
entries carry only the four event-derived fields — asserted against the
served payload); /api/census serves unsigned requests and 503s before the
first cycle; smoke publishes kind-4/kind-1059 fixtures and asserts the
census counts, the stranger's presence in all_authors, and the
no-classification-fields invariant; `make bytecheck` passes.

## Standing orders
- After any change to elevation logic, re-run the elevation privacy tests.
- Never let a network failure shrink citizens (ledger + follows.json are
  truth).
- Wards in public output = release-blocking bug. Grep public projections
  in tests, not by eye. Census corollary: a ward's PUBKEY appearing as an
  ordinary author is fine (event-derived, anonymously queryable anyway);
  any census field computed from the full ward-inclusive citizen set is
  the same release-blocking bug.
- Keep towncrier's byte budget: `wc -c` in CI, fail over 60KB.
- steward state is pubkeys, timestamps, admin actions. Event ids as
  PROVENANCE (the follows-snapshot source) are required; event ids as
  retention/protection TARGETS are forbidden. Any feature that wants a
  stored event id as a target gets written to DECISIONS.md as rejected,
  not implemented.
- All `strfry delete` calls go through the single strfry-CLI wrapper;
  raid.go is the only permitted call site. A second call site is a design
  bug, not a convenience.
- The castle never gates writes. Any feature that wants a write-path
  decision gets written to DECISIONS.md as rejected and pointed at the
  free writePolicy slot.
- When a decision isn't covered by CLAUDE.md, choose the option with less
  code, and note it in DECISIONS.md.
