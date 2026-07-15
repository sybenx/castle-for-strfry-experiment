# The Castle — a Pyramid-style retention sidecar for strfry

## What this is

A sidecar daemon + one static web page for an existing strfry relay running
in Docker. It turns an open relay into a "castle": permanent storage for the
owner (the Lord) and everyone he elevates — an invite tree of trusted members
(like fiatjaf's Pyramid relay), his follows, and his favorites and wards —
plus open lands (the Outer Lands) where anyone may write, raided at the
Lord's whim. Three functions, nothing else:

1. **The invite tree** — Pyramid mechanics, members invite members.
2. **Protected retention** — tree members, the Lord's follows, and the
   elevated are never purged. Everything else ages out at the next raid.
3. **Secret favorites (wards)** — protected pubkeys invisible to the public.

**Design creed: light as a whip.** strfry and Pyramid are loved because they
are minimal. No frameworks, no build steps for the frontend, no databases
(flat files + append-only ledger), no feature that isn't in this spec. When
in doubt, leave it out. Prefer removing a flag by choosing a behavior over
keeping both code paths.

Nothing modifies strfry itself. strfry stays stock — including its
`writePolicy.plugin` slot, which the castle deliberately does NOT occupy.
The castle never gates writes; it enforces retention after storage, at raid
time. Operators who want spam filtering plug any stock strfry write-policy
plugin into the free slot, zero castle involvement. Two components:

1. **`steward`** — sidecar daemon (Go, one binary). Follows sync, the invite
   tree, elevation, raids, a small signed-request HTTP API, stats. Serves
   towncrier's static files too, so there is no separate web container.
2. **`towncrier`** — ONE static `index.html` (vanilla JS + CSS, < ~60KB
   total, no dependencies, no bundler). Public castle status + NIP-07
   sign-in.

## Commands (Claude Code: read this first every session)

```
make build          # static steward for linux/amd64 + linux/arm64
make test           # all unit + property tests
make smoke          # docker-compose scratch strfry + fixture events via nak
make bytecheck      # fails if towncrier/index.html exceeds 60KB
docker compose -f deploy/docker-compose.yml up -d   # run the stack
nak event -k 1 -c "hello" ws://localhost:7777        # publish a test event
```

Work one PLAN.md phase per session. Check off completed phases in PLAN.md.
When a decision isn't covered by this spec, choose the option with less code
and record it in DECISIONS.md.

## The retention model (core domain logic)

There is no write gating. Every event is accepted by stock strfry; the raid
decides what survives. At raid time an event is KEPT iff any of:

1. its author is a **citizen** — {Lord} ∪ tree members ∪ current follows ∪
   elevated (favorites and wards alike);
2. its author is an **evicted member inside the grace window** — evicted
   less than `OUTER_TTL_DAYS` ago (removal timestamps come from the ledger);
3. it is **younger than the cutoff** (`now − ttl_days`).

Everything else is deleted. Load-bearing subtleties:

- **Losing citizenship is not a purge.** The grace window means removal
  never wipes anyone instantly; their events age out like a stranger's only
  after the window closes. The grace window is ALWAYS `OUTER_TTL_DAYS`,
  even when a raid runs with a smaller per-raid TTL override — sliding to
  3 days to kill a spam wave must not wipe someone evicted yesterday.
- **DMs are ephemeral here, deliberately.** NIP-59 gift wraps are signed by
  random one-time keys, so the relay cannot distinguish the Lord's own DMs
  from a stranger's; all gift wraps age out at the raid. Private/permanent
  DM storage is the job of a dedicated relay (e.g. HAVEN), not the castle.
  Stated on the landing page. (See DECISIONS.md: Castle Mail, rejected.)
- Ephemeral kinds (20000–29999) are strfry's business per NIP-16; the
  castle has no write path and stores no policy about them. Ignore them in
  stats.

## The elevation model

Four sentences govern everything:

1. **Citizenship** (retention) comes from the tree, from follows, or from
   elevation.
2. **Invite rights** come from the tree only.
3. **Elevation** is one mechanism with a visibility flag: `public: true` is
   a **favorite** (starred on the public page), `public: false` is a
   **ward** (invisible everywhere public).
4. **Retention flows from citizenship, period** — no per-event protection
   exists anywhere in this system.

Rules:

- Elevation is tree-independent. Favoriting or warding someone who was
  never invited works; a favorited tree member whose branch is cut keeps
  retention and their star but loses invite rights. NO reparenting.
- Flipping visibility (ward → favorite or back) is a single ledger action,
  not remove-and-re-add.
- Re-elevating SETS the requested visibility (idempotent; a change is
  ledgered as flip-visibility, a true no-op appends nothing and returns
  success) — it never toggles blindly.

**PRIVACY INVARIANT — wards appear in NO public output.** Not the tree, not
the favored list, not `stats.json`, not any count. Public citizen counts are
computed from public components only (tree + follows + favorites); if wards
were included, subtraction would reveal their number. `GET /api/wards`
answers only Lord-signed requests. Wards live in steward's private state
files, which are never an API response.

## The invite tree (Pyramid mechanics)

- A tree rooted at the Lord, persisted as `tree.json`:
  `{"members": {"<pubkey>": {"invited_by": "<pubkey>", "invited_at": ts, "label": "optional petname"}}}`.
  The Lord is the implicit root (not stored as a member). `tree.json` is a
  materialized view — the ledger can always reconstruct it by replay.
- **Any tree member may invite**, up to `MAX_INVITES` (default 5) direct
  invitees. `MAX_DEPTH` (default 4) levels below the Lord. Config via env.
- **Removal = cutting a branch.** The Lord may remove any member; a member
  may remove only their own invitees. Removal deletes the node AND its
  entire subtree. All removals/additions are appended to the ledger with
  timestamps (the timestamps drive the eviction grace window).
- **Follows are citizens but not tree members.** Synced from the Lord's
  kind 3; they cannot invite. The Lord can "ennoble" anyone via the UI,
  adding them to the tree as his direct invitee. If the Lord unfollows an
  ennobled member, tree membership persists (tree is authoritative once
  ennobled).
- Effective citizens = {Lord} ∪ tree members ∪ current follows ∪ elevated.
  steward recomputes and writes `citizens.json` (a derived view, used at
  raid time) every cycle and immediately after any API mutation.

## steward (the sidecar)

Own container in the same compose stack. Needs: a state volume, outbound
network, and `strfry` CLI access via `docker exec` into the strfry
container. Mount `/var/run/docker.sock` — **this is root-equivalent on the
host and the steward container faces the internet; this trade is accepted
for one-DB-owner simplicity, must be stated in the README, and a socket
proxy (e.g. tecnativa/docker-socket-proxy limited to exec) is the
documented hardening option.** Interface the strfry-CLI wrapper so tests
can fake it.

Env config: `OWNER_PUBKEY` (hex), `STRFRY_CONTAINER`, `PUBLIC_RELAYS`
(comma-sep), `OUTER_TTL_DAYS=30`, `CYCLE_MINUTES=10`, `RAID_CRON=""`
(empty = manual raids only, the default), `RAID_DRY_RUN=true` (default ON
for first deploy), `MAX_INVITES=5`, `MAX_DEPTH=4`, `LISTEN=:8787`.

### Durable state (the invariant)

**steward state contains pubkeys, timestamps, and admin actions only. Event
ids must never be stored as retention or protection TARGETS ("keep this
note") — any feature that wants one is out of scope. Event ids as
PROVENANCE (the kind-3 a follows snapshot came from) are required: they are
what makes state auditable.**

- `ledger.jsonl` — append-only, the durable source of truth (Nostr events
  age off relays; never rebuild state purely from live queries). One line
  per action: invite, remove, ennoble, elevate (with public flag), lower,
  flip-visibility, raid-run (with ttl_days used, override-or-default, and
  purge count). Every line carries `"v":1`, a source (API request id) and a
  timestamp. Replay fails loudly on unknown versions or verbs.
- `follows.json` — last-good snapshot of the Lord's kind-3 pubkey list plus
  the source event id and created_at. Exists so a steward restart during a
  network outage cannot shrink the citizenry. Only ever replaced by a
  *newer* kind 3.
- Everything else (`tree.json`, `citizens.json`, `stats.json`, kind-0 name
  cache) is a derived, regenerable view. All state-file writes are atomic
  (temp file in the SAME directory + rename).

### Cycle loop (every CYCLE_MINUTES)

1. **Follows sync.** Fetch the Lord's kind 3 (own relay + PUBLIC_RELAYS,
   newest wins). On fetch failure keep previous — never shrink on error.
2. **Merge.** Effective citizens = replay(ledger) + follows. Write
   `citizens.json`, `tree.json` atomically.
3. **Stats.** Write `stats.json` (schema below). Counts via batched
   `strfry scan --count`; cached, exactness not critical.

The cycle's steady state is nearly inert: one kind-3 fetch, done. No other
standing network behavior exists.

### The Raid (manual `POST /api/raid`, or `RAID_CRON` if set)

```
ttl_days = manual override if provided, else OUTER_TTL_DAYS
cutoff   = now − ttl_days
strfry scan '{"until": cutoff}'          # stream, don't slurp
keep if author ∈ citizens
keep if author evicted < OUTER_TTL_DAYS ago   # grace NEVER follows the override
delete the rest, batched, through the wrapper
```

- The override comes from the API body `{ttl_days: int ≥ 1}`. Clamp:
  reject 0, negative, or non-integer with 400, nothing runs. Scheduled
  (RAID_CRON) raids always use the standing OUTER_TTL_DAYS.
- **Dry-run is the preview.** `{dry_run: true, ttl_days: N}` runs the full
  scan without deleting and returns `{events}`. towncrier's raid control:
  days input (pre-filled with OUTER_TTL_DAYS) → Preview button → confirm
  dialog in plain words ("purge stranger events older than N days —
  N events") → raid. Preview is a full scan (seconds on a home DB),
  triggered by click. `RAID_DRY_RUN=true` (the default) means the armed
  raid itself also only dry-runs; the README tells the operator to review
  a dry-run log before arming deletion.
- The ledger raid-run line records events purged and the ttl_days actually
  used (override or default).
- **All `strfry delete` calls go through one wrapper with ONE call site
  (raid.go).** The only irreversible operation gets one choke point where
  dry-run, batching, and audit logging live. A second call site is a
  design bug.
- Big-DB pressure is handled by the human, not the machine: stats.json
  carries the Outer Lands event count, oldest-event age, and last-raid
  timestamp; the Lord checks DB size himself. Note: LMDB never shrinks —
  the file stays at high-water-mark size after deletion, so present raid
  results as "events purged / space reclaimed for reuse," never as disk
  shrink.

### HTTP API

steward serves on `LISTEN`: towncrier static files at `/`, JSON API at
`/api`.

Auth: **NIP-98** — `Authorization: Nostr <base64 kind-27235 event>` signed
in the browser via NIP-07. Verify signature, `u` tag matches full URL,
`method` tag matches, created_at within ±60s. Replay guard: remember event
ids for 5 minutes (in-memory). Identity = event pubkey. No sessions, no
cookies.

- `GET  /api/stats` — public. `stats.json`.
- `GET  /api/tree` — public. Tree with kind-0 names/pictures resolved by
  steward and favorite stars, plus a `favored` array of non-tree favorites
  and names for the evicted list. The name cache covers tree members ∪
  public favorites ∪ evicted members inside their grace window — never
  wards.
- `GET  /api/wards` — **Lord only.** The ward list.
- `POST /api/invite {pubkey, label?}` — tree members + Lord. Enforce
  MAX_INVITES / MAX_DEPTH. Accept npub or hex everywhere a pubkey is
  accepted.
- `POST /api/remove {pubkey}` — Lord: anyone; member: own invitees only.
  Cuts the subtree; records eviction timestamps for the grace window.
- `POST /api/ennoble {pubkey}` — Lord only.
- `POST /api/elevate {pubkey, public}` — Lord only. Favorite (public) or
  ward (private).
- `POST /api/lower {pubkey}` — Lord only. Removes elevation.
- `POST /api/raid {ttl_days?, dry_run?}` — Lord only.

Every mutation: append to ledger, rewrite state files immediately (no
waiting for the next cycle). Rate-limit the API per IP. CORS: same-origin
only.

### Name cache and update banner

Kind-0 name/avatar cache for tree members, public favorites, and evicted
members inside their grace window (local relay first, PUBLIC_RELAYS
fallback; atomic cache file; lazy refresh with a staleness threshold;
never wards). Once a day steward checks the project's GitHub releases for
a newer tag and exposes it in `stats.json`; towncrier shows the Lord a
banner with the one-line update command. No self-updating machinery in v1
(see DECISIONS.md).

**Version comparison is steward's job, not towncrier's.** steward knows
its own build-stamping conventions (`git describe`, ldflags, the `dev`
fallback); towncrier only renders what steward decides. steward normalizes
`version.running`/`version.latest` (or adds a derived signal) so that
"is an update actually available" is answered once, in Go, under test —
never inferred client-side from raw tag strings via regex.

### The Census

Public DB-transparency layer, added by formal re-scope (see DECISIONS.md,
"The Census re-scope"). Governing rule: **the public surface may display
only what an anonymous client could discover by querying the relay
directly.** strfry has no read gating, so authors, kinds, counts, and
timestamps are all anonymously derivable and may be public. Nothing in the
census is ever computed from steward's private state.

- Every cycle, the SAME full scan that already counts the Outer Lands also
  aggregates per-author `{events, first_seen, last_seen}`, per-kind
  totals, and encrypted-event counts (kinds 4 + 1059, aggregate only —
  gift wraps are signed by one-time keys, attribution is impossible even
  in principle). Ephemeral kinds (20000–29999) are excluded. Written
  atomically to `census.json` (a derived, regenerable view).
- `GET /api/census` — public, unauthenticated, like `/api/stats`.
- **Ward nuance:** ward pubkeys MAY appear in the census as ordinary
  authors (their events are on an open relay; unavoidable), but never
  with any status marker. Any citizen/stranger split in census output is
  computed from PUBLIC components only (tree ∪ follows ∪ favorites ∪ the
  Lord — `publicCitizenPubkeys`); under it a ward classifies as an
  ordinary outer author, exactly what an anonymous observer sees. Using
  the full (ward-inclusive) citizen set anywhere in census output is a
  release-blocking bug, same severity as a ward in stats.json.
- towncrier renders capped views only (top authors, longest-quiet outer
  authors, totals, kind and encrypted counts) plus a download link to the
  full census JSON — never the entire author list as DOM.
- **The event viewer**: towncrier can show the events this relay stores
  for one pubkey — a plain NIP-01 `REQ` over websocket straight to the
  relay, rendered as raw escaped text, capped (50 events, truncated
  content). Deliberately unauthenticated: the relay answers those queries
  for anyone, so gating the viewer would protect nothing. Raw text only —
  no media, no threads, no reactions; the moment it grows client
  features, it has become the rejected "towncrier as a feed" and must be
  cut back.

## towncrier (the page)

ONE `index.html`. Vanilla JS, hand-written CSS, dark castle aesthetic,
small inline SVG castle. No framework, no CDN fonts, no analytics, no
build. Target < 60KB total, enforced in CI. Wards must never appear, in
any form, including in counts.

Public view:
- **The Lord** — linked npub, resolved name/avatar.
- **The Court** — the invite tree as nested `<details>` elements (free
  collapse/expand, zero JS), avatars + names, "invited by" lineage, and a
  star glyph on favorites. When the Lord is signed in the stars become
  clickable toggles inline. This is the Pyramid centerpiece.
- **Favored of the Lord** — favorites who are not tree members. Fed by
  /api/tree's `favored` array.
- **The Citizenry** — counts (tree + follows + favorites ONLY), events
  stored.
- **The Evicted** — recent removals still in their grace window, names
  struck through with their expiry date (or "until the next raid" when
  raids are manual).
- **The Outer Lands** — event count, oldest event age, next raid ("at the
  Lord's pleasure" when RAID_CRON is empty, countdown otherwise), last
  raid's purge count, days since last raid.
- A one-line note that DMs are ephemeral here (see retention model).
- Footer: relay URL (click to copy), NIP-11 fields
  (`fetch(origin, {headers:{Accept:'application/nostr+json'}})`), repo
  link.
- Member rows link to the member's profile on njump. Towncrier is a status
  board, NEVER a feed — rendering notes would make it a Nostr client and
  blow the byte budget.

Signed-in view (one "Enter the castle" button → NIP-07):
- Tree member: "Invite" (npub paste + optional petname; remaining invites
  shown), "Remove" on own invitees (confirm dialog warns the whole branch
  falls, and that evicted notes survive one grace period).
- The Lord: additionally Remove on anyone, Ennoble, star toggles, the raid
  control (days input → Preview → confirm → raid), the update banner, and
  **the Wards view** — a section rendered only for the Lord (data comes
  only from Lord-authenticated `GET /api/wards`) listing current wards, an
  npub field to add one, lower buttons, and visibility-flip buttons. UI
  tooltip on stars: "a favorite is public honor AND protection; a ward is
  the same protection, unseen."
- If window.nostr is absent, the button explains NIP-07 and links common
  extensions. Signed-in state is a pubkey in a JS variable; each action
  signs a fresh NIP-98 event. No persistence.

`stats.json` schema:

```json
{
  "updated_at": 1730000000,
  "version": {"running": "v0.3.0", "latest": "v0.3.1", "status": "update"},
  "the_lord": {"pubkey": "<hex>", "events": 12345},
  "citizens": {"tree": 47, "follows": 812, "favored": 6, "events": 480211},
  "evicted": [{"pubkey": "<hex>", "expires": 1730073600}],
  "outer_lands": {"events": 231998, "oldest": 1727400000, "ttl_days": 30},
  "raids": {"next": null, "last_at": 1729987200, "last_purged": 5410},
  "invites": {"max_per_member": 5, "max_depth": 4}
}
```

(`raids.next` is null when RAID_CRON is empty. Ward counts appear nowhere.)

## Reverse proxy

Document for both Caddy and nginx (setups vary):
- WebSocket upgrades AND `Accept: application/nostr+json` → strfry:7777
- everything else → steward:8787 (towncrier + /api)
- Forward a real-IP header so steward's API rate limit sees real clients.

## Deployment

`docker-compose.yml` fragment for the existing stack: one new `steward`
service with a `castle-state` volume and the docker.sock mount. **No
strfry.conf changes, no plugin volume, no files placed inside the strfry
container.** The writePolicy slot stays free for any spam-filter plugin
the operator chooses. Support amd64 + arm64 (plain GOARCH cross-compile).
First deploy runs with RAID_DRY_RUN=true and manual raids.

## Repo layout

```
castle/
  CLAUDE.md  PLAN.md  DECISIONS.md
  Makefile
  steward/
    main.go cycle.go raid.go ledger.go tree.go elevation.go
    api.go nostr.go stats.go
  towncrier/index.html
  deploy/docker-compose.yml deploy/Caddyfile deploy/nginx.conf
  .env.example
```

steward uses github.com/nbd-wtf/go-nostr.

## Testing checklist

- tree: invite respects MAX_INVITES/MAX_DEPTH; member can't remove
  non-invitee; Lord removes anyone; cut branch drops whole subtree;
  ennobled follow persists after unfollow; property test — ledger replay
  always reconstructs identical tree + elevation state.
- elevation: elevated non-member is a citizen; cut-branch favorite keeps
  retention, loses invite rights; visibility flip is one ledger line;
  re-elevating same visibility appends nothing and returns success; wards
  absent from /api/tree, /api/stats, and all public counts (grep the
  served payloads in a test, not by eye).
- steward: follows never shrink on fetch error and survive restart via
  follows.json; every mutation appears in ledger and state files
  immediately; every ledger line carries v:1; replay rejects unknown
  versions loudly.
- raid: citizen + elevated survive; 31-day stranger note deleted; evicted
  member's notes survive inside the grace window and die after it; an
  evicted member inside their OUTER_TTL_DAYS grace survives a raid run
  with a SMALLER override; override respected; absent override uses
  default; ttl_days=0 rejected; ledger line records the ttl used; dry-run
  deletes nothing and returns a nonzero event count against the fixture
  stack.
- API: bad NIP-98 sig rejected; stale created_at rejected; replayed event
  id rejected; `u`/`method` mismatch rejected; /api/wards refuses
  non-Lord.
- Smoke: compose up scratch strfry, publish fixtures with nak, assert via
  strfry scan; drive the API with curl + nak-signed NIP-98 headers.

## Hard-won context (do not "simplify" these away)

- NIP-59 gift wraps use random one-time signing keys — the relay cannot
  attribute DMs to anyone. That is WHY DMs are ephemeral here and why no
  amount of author-list cleverness can protect them. Do not rebuild Castle
  Mail piecemeal; it was cut deliberately (DECISIONS.md).
- The ledger exists because events age off relays; there is no protocol
  source of truth for admin actions. Never rebuild state purely from live
  queries.
- **Thread context and archival-of-everything are delegated to the Lord's
  Chronicle relay. Do not add promotion, context-fetching, backfill, or
  per-event protection mechanisms here.**
- **The raid is the moderation.** Spam is not judged, it is outlived. The
  Lord's value judgment is elevation (follow, invite, favorite, ward).
  Write-path filtering belongs to third-party plugins in strfry's free
  writePolicy slot, never to the castle.
- strfry has no read gating. Truly private storage is delegated elsewhere;
  ward privacy is obscurity, not cryptography: raids at whim give no clean
  TTL to fingerprint, and that is deemed sufficient.
- The invite tree is the accountability mechanism (lineage), not a growth
  mechanism. Resist any feature that weakens "your invitees are your
  responsibility."
- Favorites and wards are ONE mechanism differing only in visibility.
  Resist structural divergence between them.

## Distribution (first-class deliverables, not afterthoughts)

- **Releases via GitHub Actions**: on tag push, build a static `steward`
  for linux/amd64 + linux/arm64 (CGO_ENABLED=0), attach to a GitHub
  Release with sha256 checksums, push a multi-arch image to ghcr.io.
- **`install.sh`** (curl-pipe-able, idempotent, re-runnable) — downloads
  and verifies, detects arch and a running strfry container (confirm with
  the user), prompts for OWNER_PUBKEY (npub or hex), creates the
  `castle-state` volume, writes `.env` (RAID_DRY_RUN=true, RAID_CRON
  empty). **It PRINTS and never edits:** the compose service snippet and
  the reverse-proxy config for Caddy and nginx. Auto-editing an unknown
  Umbrel/Portainer stack is how installs brick relays.
- **`uninstall.sh`**: prints the lines to remove, removes the binaries,
  leaves the state volume with a note on how to delete it.
- README: one-line install front and center; manual steps below; the
  docker.sock risk disclosure; a note that the writePolicy slot is free
  for spam plugins; a screenshot of towncrier.
