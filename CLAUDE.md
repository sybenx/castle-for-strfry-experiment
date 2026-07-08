# The Castle — a Pyramid-style tiered relay system for strfry

## What this is

A sidecar + write-policy plugin + interactive web UI for an existing strfry
relay running in Docker (Portainer, on Umbrel). It turns a fully open relay
into a "castle": permanent storage for the owner (the Lord) and everyone he
elevates — an invite tree of trusted members (like fiatjaf's Pyramid relay),
his follows, and his chosen favorites and wards — plus open lands (the Wild
West) where anyone may write, raided at the Lord's whim. Ephemeral DMs work
for everyone; they are permanent only for the castle.

**Design creed: light as a whip.** strfry and Pyramid are loved because they
are minimal. Every component here must honor that: no frameworks, no build
steps for the frontend, no databases (flat files + append-only ledger), no
feature that isn't in this spec. When in doubt, leave it out. Simplify
mechanisms, not requirements — and prefer removing a flag by choosing a
behavior over keeping both code paths.

Nothing modifies strfry itself. strfry stays stock. Three components:

1. **`gatekeeper`** — strfry write-policy plugin (Go, static binary, stdlib
   only). O(1) accept/reject per event from in-memory hashsets. No network.
2. **`steward`** — sidecar daemon (Go, one binary). Follows sync, report
   intake, raids, the invite tree, elevation, manual archival, a small
   signed-request HTTP API, NIP-05 serving, stats. Serves towncrier's static
   files too, so there is no separate web container.
3. **`towncrier`** — ONE static `index.html` (vanilla JS + CSS, < ~60KB total,
   no dependencies, no bundler). Public castle status + NIP-07 sign-in.

## Commands (Claude Code: read this first every session)

```
make build          # static gatekeeper + steward for linux/amd64 + linux/arm64
make test           # all unit + property tests
make smoke          # docker-compose scratch strfry + fixture events via nak
make bytecheck      # fails if towncrier/index.html exceeds 60KB
docker compose -f deploy/docker-compose.yml up -d   # run the stack
nak event -k 1 -c "hello" ws://localhost:7777        # publish a test event
```

Work one PLAN.md phase per session. Check off completed phases in PLAN.md.
When a decision isn't covered by this spec, choose the option with less code
and record it in DECISIONS.md.

## The tier system (core domain logic)

Every event falls into exactly one tier. First match wins, top to bottom:

| Tier | Who/what | Write | Retention |
|---|---|---|---|
| **Outlaws** | pubkey in ban set | REJECT | existing events purged immediately |
| **The Lord** | `OWNER_PUBKEY` | accept | permanent |
| **Castle Mail** | kind 1059 (gift wrap) or 9735 (zap receipt) with a `p` tag of the Lord or any citizen | accept | permanent |
| **Citizens** | tree members ∪ the Lord's follows ∪ the elevated | accept | permanent while citizen |
| **Wild West** | everyone else | accept (rate-limited) | purged at the next raid if older than `COURTYARD_TTL_DAYS` |

Load-bearing subtleties — do not lose these:

- **Castle Mail is judged by recipient, not author.** NIP-59 gift wraps are
  signed by random one-time keys; author-based rules are blind to them. Match
  kind ∈ {1059, 9735} AND any `p` tag ∈ (Lord ∪ citizens). This rule precedes
  author rules and is exempt from pruning and from the rate limiter. Without
  it the Lord's own DMs get rejected or purged. Stranger-to-stranger gift
  wraps match no rule, land in Wild West, and age out — that is the
  "ephemeral DMs for everyone" behavior, and it is intentional.
- **Ban check precedes everything.** steward must refuse to ever ban
  `OWNER_PUBKEY`.
- **Losing citizenship is not a purge.** An evicted member's events get a
  grace period: the raid keeps events whose author was evicted less than
  `COURTYARD_TTL_DAYS` ago (removal timestamps come from the ledger). After
  the grace window their events age out like any stranger's. Explicit bans
  DO purge immediately — that is the difference between exile and outlawry.
- **Domain checking is steward-side only and asynchronous.** gatekeeper never
  sees domains; steward resolves banned domains to pubkeys (see Ban intake)
  and only pubkeys ever reach `banned.json`. gatekeeper stays O(1).
- Ephemeral kinds (20000–29999) pass through per NIP-16 (strfry doesn't
  store them) but are NOT exempt from the write path: non-citizen ephemeral
  traffic goes through the same per-IP token bucket as any stranger event.
  Citizens are exempt as always. Ignore ephemeral kinds in stats.

## The elevation model

Four sentences govern everything:

1. **Citizenship** (retention + rate-limit exemption) comes from the tree,
   from follows, or from elevation.
2. **Invite rights** come from the tree only.
3. **Elevation** is one mechanism with a visibility flag: `public: true` is a
   **favorite** (starred on the public page), `public: false` is a **ward**
   (invisible everywhere public).
4. **Retention flows from citizenship, period** — no per-event protection
   exists anywhere in this system.

Rules:

- Elevation is tree-independent. Favoriting or warding someone who was never
  invited works; a favorited tree member whose branch is cut keeps retention
  and their star but loses invite rights. There is NO reparenting mechanism.
- **Ban beats elevation.** Banning an elevated pubkey requires a double
  confirmation in the UI ("this pubkey is favored/warded — outlaw them
  anyway?") and removes the elevation.
- Flipping visibility (ward → favorite or back) is a single ledger action,
  not remove-and-re-add.
- **React-warding:** each cycle, steward fetches the Lord's kind-7 reactions
  since a watermark, resolves each reacted note's author from the local
  relay, and wards that author (ledger source = the reaction event id).
  Reactions are already public events, so this leaks nothing; only the
  retention is silent. React-created wards are listed on the ward page with
  their source and can be lowered like any other.

**PRIVACY INVARIANT — wards appear in NO public output.** Not the tree, not
the favored list, not `stats.json`, not any count. Public citizen counts are
computed from public components only (tree + follows + favorites); if wards
were included, subtraction would reveal their number. `GET /api/wards`
answers only Lord-signed requests. `citizens.json` includes wards (gatekeeper
must accept their events) but is a shared-volume file, never an API response.

## The invite tree (Pyramid mechanics)

- A tree rooted at the Lord, persisted as `tree.json`:
  `{"members": {"<pubkey>": {"invited_by": "<pubkey>", "invited_at": ts, "label": "optional petname"}}}`.
  The Lord is the implicit root (not stored as a member). `tree.json` is a
  materialized view — the ledger can always reconstruct it by replay.
- **Any tree member may invite**, up to `MAX_INVITES` (default 5) direct
  invitees. `MAX_DEPTH` (default 4) levels below the Lord. Config via env.
- **Removal = cutting a branch.** The Lord may remove any member; a member may
  remove only their own invitees. Removal deletes the node AND its entire
  subtree. All removals/additions are appended to the ledger with timestamps
  (the timestamps drive the eviction grace window).
- **Follows are citizens but not tree members.** They are synced from the
  Lord's kind 3 and cannot invite. The Lord can "ennoble" anyone via the UI,
  adding them to the tree as his direct invitee. If the Lord unfollows an
  ennobled member, tree membership persists (tree is authoritative once
  ennobled).
- Banning a tree member cuts their branch.
- Effective citizens = {Lord} ∪ tree members ∪ current follows ∪ elevated.
  steward recomputes and writes `citizens.json` every cycle and immediately
  after any API mutation.

## Component 1: gatekeeper (write-policy plugin)

- Go, CGO_ENABLED=0, stdlib + encoding/json only. "Stdlib-only" means no
  external dependencies — the shared `internal/stateformat` package (itself
  stdlib-only) that defines the banned.json/citizens.json types is the one
  permitted import, precisely so steward's writers and gatekeeper's reader
  can never drift. Runs inside the strfry container via strfry.conf
  `writePolicy.plugin = "/plugin/gatekeeper"`.
- strfry plugin protocol: JSONL on stdin
  (`{"type":"new","event":{...},"sourceInfo":"<ip>"}`), respond
  `{"id":"<event id>","action":"accept"|"reject","msg":"..."}` on stdout,
  flush per line. NOTHING else on stdout; diagnostics to stderr. A malformed
  input line must not kill the loop. Add a Go native fuzz target for the
  stdin loop.
- Reads `banned.json` and `citizens.json` from the shared volume; stats mtime
  ≤ 1/sec (poll interval injectable, so tests don't depend on wall-clock);
  hot-reloads into hashsets. Missing files = empty sets (fail open).
- Per-IP token bucket for non-citizen, non-Castle-Mail events: default
  30 events/min, burst 60. Evict buckets idle > 10 minutes (unbounded IP
  churn must not grow memory forever).
- **`sourceInfo` is only meaningful if strfry is configured with
  `relay.realIpHeader` behind the reverse proxy** — otherwise every writer
  shares the proxy's IP and the limiter is either a no-op or a self-DoS.
  Document in deploy/, verify in the smoke test.
- Themed reject messages: banned → `blocked: you have been exiled from these
  lands`; rate limit → `rate-limited: the courtyard is busy`.
- Test fixtures (`citizens.json`, `banned.json`, event lines) are committed
  under `gatekeeper/testdata/`.

## Component 2: steward (sidecar daemon)

Own container in the same compose stack. Needs: shared state volume, outbound
network, and `strfry` CLI access via `docker exec` into the strfry container.
Mount `/var/run/docker.sock` — **this is root-equivalent on the host and the
steward container faces the internet; this trade is accepted for one-DB-owner
simplicity, must be stated in the README, and a socket proxy (e.g.
tecnativa/docker-socket-proxy limited to exec) is the documented hardening
option.** Interface the strfry-CLI wrapper so tests can fake it.

Env config: `OWNER_PUBKEY` (hex), `STRFRY_CONTAINER`, `PUBLIC_RELAYS`
(comma-sep), `COURTYARD_TTL_DAYS=30`, `CYCLE_MINUTES=10`,
`RAID_CRON=""` (empty = manual raids only, the default), `RAID_DRY_RUN=true`
(default ON for first deploy), `MAX_INVITES=5`, `MAX_DEPTH=4`,
`NIP05_DOMAIN` (optional; enables serving `/.well-known/nostr.json`),
`LISTEN=:8787`.

### Durable state (the invariant)

**steward state contains pubkeys, timestamps, and admin actions only — never
event ids. Any feature requiring a stored event id is out of scope.**

- `ledger.jsonl` — append-only, the durable source of truth (Nostr events age
  off relays; never rebuild state purely from live queries). One line per
  action: invite, remove, ennoble, ban, pardon, ban-domain, pardon-domain,
  elevate (with public flag), lower, flip-visibility, archive-run,
  raid-run (with purge counts). Every line carries source (event id of the
  triggering report/reaction, or API request id) and timestamp.
- `follows.json` — last-good snapshot of the Lord's kind-3 pubkey list plus
  the source event id and created_at. Exists so a steward restart during a
  network outage cannot shrink the citizenry. Only ever replaced by a
  *newer* kind 3.
- Everything else (`tree.json`, `citizens.json`, `banned.json`, `stats.json`,
  kind-0 name cache, react watermark) is a derived, regenerable view. All
  state-file writes are atomic (temp file in the SAME directory + rename).

### Cycle loop (every CYCLE_MINUTES)

1. **Follows sync.** Fetch the Lord's kind 3 (own relay + PUBLIC_RELAYS,
   newest wins). On fetch failure keep previous — never shrink on error.
2. **Report intake.** Fetch kind 1984 authored by OWNER_PUBKEY. p-tags with
   report type `spam`, `illegal`, or `malware` → ban pubkey. Content line
   `ban-domain: <domain>` → ban domain. Other report types ignored. There is
   NO kind-5 void handling and NO pardon-list sync — undo happens only via
   the web UI pardon (one undo path, the ledger).
3. **React-warding.** Fetch the Lord's kind 7 since the react watermark; ward
   the authors of reacted notes (resolved from the local relay); bump the
   watermark.
4. **Ledger & merge.** Effective sets = replay(ledger) + follows. Write
   `banned.json`, `citizens.json`, `tree.json` atomically.
5. **Purge newly banned.** `strfry delete --filter '{"authors":[...]}'`,
   ≤50 authors per call.
6. **Stats.** Write `stats.json` (schema below). Counts via batched
   `strfry scan --count`; cache, exactness not critical.

The cycle's steady state is nearly inert: kind 3, kind 1984, kind 7, done.
No other standing network behavior exists.

### Ban intake and domains

- Only reports SIGNED BY OWNER_PUBKEY count (NIP-56 warns relays not to
  auto-moderate on public reports — gameable). Never widen intake without an
  explicit trust list.
- Banned domains are ledgered. **At each raid** (not each cycle): re-fetch
  `https://<domain>/.well-known/nostr.json` for every banned domain (5s
  timeout, errors ignored), ban every listed pubkey; sweep local kind-0
  events for `nip05` claims of banned domains and ban the claimants.
  Re-enumeration catches spam farms registering fresh pubkeys under the
  same domain. NIP-05 is self-asserted; domain bans are a convenience, not a
  security boundary — pubkey bans are the backbone.
- Web UI provides ban (pubkey or domain) and pardon, Lord only, through the
  same ledger path as report-driven bans.

### The Raid (manual `POST /api/raid`, or `RAID_CRON` if set)

```
cutoff = now - COURTYARD_TTL_DAYS
re-enumerate banned domains; sweep kind-0 nip05 claims   (see above)
strfry scan '{"until": cutoff}'   # stream, don't slurp
  delete events UNLESS:
    author ∈ citizens                                  (includes elevated)
    OR (kind ∈ {1059,9735} AND any p-tag ∈ citizens)   (Castle Mail)
    OR (author evicted at T where now - T < COURTYARD_TTL_DAYS)  (grace)
  → strfry delete by id, batches of 1000
```

(strfry delete filters can't express negation; scan-then-delete is required.)
Honor `RAID_DRY_RUN`. Log purge counts to the ledger for stats. Raids default
to manual — "at the Lord's pleasure."

### Manual archival (the scribe)

No automatic backfill of anyone, ever. The Lord clicks "Archive notes" on a
member → `POST /api/archive {pubkey}` → a one-shot background job:

- Paginated REQ per relay in `PUBLIC_RELAYS` (most public strfry deployments
  have negentropy sync disabled, so plain pagination it is):
  `{"authors":[pk],"until":cursor,"limit":500}`, cursor = oldest created_at
  seen, repeat until a short page. Polite pacing (delay between pages, one
  relay at a time, backoff on CLOSED).
- Fetched events are citizen-signed, so gatekeeper accepts them: **replay
  them into the castle over a local websocket** — no strfry import, no
  docker exec on this path.
- Pagination is inherently leaky (shared timestamps, silent relay caps).
  This is best-effort archival, not a completeness guarantee. Do NOT build
  reconciliation machinery.
- One job at a time; progress/completion logged to the ledger (counts only).
- The scribe runs in its own goroutine with its own failure domain — a scribe
  crash must never touch the cycle.

### HTTP API

steward serves on `LISTEN`: towncrier static files at `/`, JSON API at
`/api`, and (if `NIP05_DOMAIN` set) `/.well-known/nostr.json` mapping tree
members' labels to their pubkeys (castle membership as identity).

Auth: **NIP-98** — `Authorization: Nostr <base64 kind-27235 event>` signed in
the browser via NIP-07. Verify signature, `u` tag matches full URL, `method`
tag matches, created_at within ±60s. Replay guard: remember event ids for
5 minutes (in-memory). Identity = event pubkey. No sessions, no cookies.

- `GET  /api/stats` — public. `stats.json`.
- `GET  /api/tree` — public. Tree with kind-0 names/pictures resolved by
  steward (cached, refreshed lazily, tree members only) and favorite stars.
- `GET  /api/wards` — **Lord only.** The ward list with sources.
- `POST /api/invite {pubkey, label?}` — tree members + Lord. Enforce
  MAX_INVITES / MAX_DEPTH / not-banned (banned targets require pardon first).
  Accept npub or hex everywhere a pubkey is accepted.
- `POST /api/remove {pubkey}` — Lord: anyone; member: own invitees only.
  Cuts the subtree; records eviction timestamps for the grace window.
- `POST /api/ennoble {pubkey}` — Lord only.
- `POST /api/elevate {pubkey, public}` — Lord only. Favorite (public) or
  ward (private). Re-elevating flips visibility.
- `POST /api/lower {pubkey}` — Lord only. Removes elevation.
- `POST /api/ban {pubkey|domain}` / `POST /api/pardon {pubkey|domain}` —
  Lord only. OWNER_PUBKEY is unbannable.
- `POST /api/archive {pubkey}` — Lord only. Starts a scribe job.
- `POST /api/raid` — Lord only. Manual raid trigger.

Every mutation: append to ledger, rewrite state files immediately (no waiting
for the next cycle), gatekeeper hot-reloads within a second. Rate-limit the
API per IP. CORS: same-origin only.

### Update banner

Once a day steward checks the project's GitHub releases for a newer tag and
exposes it in `stats.json`. Towncrier shows the Lord a banner with the
one-line update command. No self-updating machinery in v1 (see DECISIONS.md).

## Component 3: towncrier (the page)

ONE `index.html`. Vanilla JS, hand-written CSS, dark castle aesthetic, small
inline SVG castle. No framework, no CDN fonts, no analytics, no build.
Target < 60KB total, enforced in CI. Everything on the public page is
information already public via CLI queries — except that wards must never
appear, in any form, including in counts.

Public view:
- **The Lord** — linked npub, resolved name/avatar.
- **The Court** — the invite tree as nested `<details>` elements (free
  collapse/expand, zero JS), avatars + names, "invited by" lineage, and a
  star glyph on favorites. When the Lord is signed in the stars become
  clickable toggles inline. This is the Pyramid centerpiece.
- **Favored of the Lord** — favorites who are not tree members.
- **The Citizenry** — counts (tree + follows + favorites ONLY), events stored.
- **The Vault** — castle mail count.
- **The Evicted** — recent removals still in their grace window, names struck
  through with their expiry date (or "until the next raid" when raids are
  manual).
- **The Wild West** — event count, oldest event age, next raid ("at the
  Lord's pleasure" when RAID_CRON is empty, countdown otherwise), last raid's
  purge count.
- **The Exiled** — banned pubkey count, banned domains listed by name.
- Footer: relay URL (click to copy), NIP-11 fields
  (`fetch(origin, {headers:{Accept:'application/nostr+json'}})`), repo link.
- Member rows link to the member's profile on njump. Towncrier is a status
  board, NEVER a feed — rendering notes would make it a Nostr client and
  blow the byte budget.

Signed-in view (one "Enter the castle" button → NIP-07):
- Tree member: "Invite" (npub paste + optional petname; remaining invites
  shown), "Remove" on own invitees (confirm dialog warns the whole branch
  falls, and that evicted notes survive one grace period).
- The Lord: additionally Remove on anyone, Ennoble, star toggles, Ban /
  Pardon (with a domain field), Archive buttons, raid-now, the update
  banner, and **the Wards view** — a section rendered only for the Lord
  (data comes only from Lord-authenticated `GET /api/wards`) listing current
  wards with sources, an npub field to add one, lower buttons, and
  visibility-flip buttons. UI tooltip on stars: "a favorite is public honor
  AND protection; a ward is the same protection, unseen."
- Banning an elevated pubkey triggers the double confirmation.
- If window.nostr is absent, the button explains NIP-07 and links common
  extensions. Signed-in state is a pubkey in a JS variable; each action signs
  a fresh NIP-98 event. No persistence.

`stats.json` schema:

```json
{
  "updated_at": 1730000000,
  "version": {"running": "v0.3.0", "latest": "v0.3.1"},
  "the_lord": {"pubkey": "<hex>", "events": 12345},
  "citizens": {"tree": 47, "follows": 812, "favored": 6, "events": 480211},
  "castle_mail": {"events": 4021},
  "evicted": [{"pubkey": "<hex>", "expires": 1730073600}],
  "wild_west": {"events": 231998, "oldest": 1727400000},
  "outlaws": {"pubkeys": 41, "domains": ["nostrmag.example"], "events_purged_total": 88213},
  "raids": {"next": null, "last_at": 1729987200, "last_purged": 5410},
  "invites": {"max_per_member": 5, "max_depth": 4}
}
```

(`raids.next` is null when RAID_CRON is empty. Ward counts appear nowhere.)

## Reverse proxy

Document for both Caddy and nginx (Umbrel setups vary):
- WebSocket upgrades AND `Accept: application/nostr+json` → strfry:7777
- everything else → steward:8787 (towncrier + /api + /.well-known)
- MUST set/forward a real-IP header and configure strfry `relay.realIpHeader`
  to match, or the gatekeeper rate limiter is meaningless.

## Deployment

`docker-compose.yml` fragment for the existing Portainer stack: `steward`
(new), shared named volume `castle-state` mounted into the strfry container
at `/plugin/`, strfry.conf change `writePolicy { plugin = "/plugin/gatekeeper" }`.
Support amd64 + arm64 (plain GOARCH cross-compile in the Makefile). First
deploy runs with RAID_DRY_RUN=true and manual raids; the README tells the
operator to review a dry-run raid's log before arming deletion.

## Repo layout

```
castle/
  CLAUDE.md  PLAN.md  DECISIONS.md
  Makefile
  gatekeeper/main.go  gatekeeper/testdata/
  steward/
    main.go cycle.go raid.go scribe.go ledger.go tree.go elevation.go
    api.go nostr.go nip05.go stats.go
  towncrier/index.html
  deploy/docker-compose.yml deploy/strfry.conf.patch deploy/Caddyfile deploy/nginx.conf
  .env.example
```

steward uses github.com/nbd-wtf/go-nostr. gatekeeper stays stdlib-only.

## Testing checklist

- gatekeeper: banned rejected; citizen accepted; ward accepted (from
  citizens.json — the file carries no visibility info); gift wrap p-tagging
  a citizen accepted from unknown author and not rate-limited; stranger
  rate-limited after burst; malformed line survives; fuzz target passes;
  hot-reload within one poll interval; bucket eviction works.
- tree: invite respects MAX_INVITES/MAX_DEPTH; member can't remove
  non-invitee; Lord removes anyone; cut branch drops whole subtree; ennobled
  follow persists after unfollow; banning a member cuts their branch;
  property test — ledger replay always reconstructs identical tree +
  elevation state.
- elevation: elevated non-member is a citizen; cut-branch favorite keeps
  retention, loses invite rights; visibility flip is one ledger line; ban
  removes elevation; react-warding wards the reacted author exactly once;
  wards absent from /api/tree, /api/stats, and all public counts.
- API: bad NIP-98 sig rejected; stale created_at rejected; replayed event id
  rejected; `u`/`method` mismatch rejected; /api/wards refuses non-Lord;
  every mutation appears in ledger and state files immediately;
  OWNER_PUBKEY unbannable.
- steward: spam/illegal/malware report bans, nudity report doesn't; pardon
  via API unbans; follows never shrink on fetch error and survive restart
  via follows.json; `ban-domain:` convention; raid-time re-enumeration and
  kind-0 sweep.
- raid: citizen + elevated + castle mail survive; 31-day stranger note
  deleted; stranger-to-stranger gift wrap older than TTL deleted; gift wrap
  p-tagging a citizen survives at any age; evicted member's notes survive
  inside the grace window and die after it; dry-run deletes nothing.
- scribe: paginates to completion on a fixture relay; polite pacing; crash
  does not affect the cycle; events land via the local websocket.
- Smoke: compose up scratch strfry, publish fixtures with nak, assert via
  strfry scan; drive the API with curl + nak-signed NIP-98 headers.

## Hard-won context (do not "simplify" these away)

- NIP-56 warns relays not to auto-moderate on public reports (gameable).
  Only reports SIGNED BY OWNER_PUBKEY count. Never widen intake.
- NIP-59 gift wraps use random one-time signing keys. Author-only whitelists
  silently kill the Lord's own DMs. The recipient-based Castle Mail rule is
  load-bearing — and its absence for strangers is what makes their DMs
  ephemeral, which is a feature.
- NIP-05 is self-asserted; domain bans are a convenience against lazy spam
  farms, not a security boundary. Pubkey bans are the backbone.
- The ledger exists because events age off relays; there is no protocol
  "unreport". Undo is the web UI pardon, one path, through the ledger.
- **Thread context and archival-of-everything are delegated to the Lord's
  Chronicle relay. Do not add promotion, context-fetching, or per-event
  protection mechanisms here.** steward state is pubkeys, timestamps, and
  admin actions only — never event ids.
- **The raid is the moderation.** Spam is not judged, it is outlived. The
  Lord's value judgment is elevation (follow, invite, favorite, ward), not
  ban-chasing. The blacklist is a scalpel for content that can't wait 30
  days and for write-path abusers; if it grows long, revisit the design —
  do not pre-build automation.
- strfry has no read gating. Truly private storage (auth-gated DM reads) is
  delegated to the Lord's existing HAVEN relay; the castle stores mail but
  cannot hide its existence. Accepted, and stated on the landing page. Ward
  privacy is obscurity, not cryptography: raids at whim give no clean TTL to
  fingerprint, and that is deemed sufficient.
- The invite tree is the accountability mechanism (lineage), not a growth
  mechanism. Resist any feature that weakens "your invitees are your
  responsibility."
- Favorites and wards are ONE mechanism differing only in visibility. Resist
  structural divergence between them.

## Distribution (first-class deliverables, not afterthoughts)

- **Releases via GitHub Actions**: on tag push, build static `gatekeeper` and
  `steward` binaries for linux/amd64 + linux/arm64 (CGO_ENABLED=0), attach to
  a GitHub Release with sha256 checksums, push a multi-arch `steward` image
  to ghcr.io. goreleaser or a plain build matrix — whichever is fewer lines.
- **`install.sh`** (curl-pipe-able, idempotent, re-runnable) — downloads and
  verifies binaries, detects arch and a running strfry container (confirm
  with the user), prompts for OWNER_PUBKEY (npub or hex), creates the
  `castle-state` volume, places gatekeeper at `/plugin/gatekeeper`, writes
  `.env` (RAID_DRY_RUN=true, RAID_CRON empty). **It PRINTS and never edits:**
  the strfry.conf writePolicy block, the compose service snippet, and the
  reverse-proxy config for Caddy and nginx. Auto-editing an unknown
  Umbrel/Portainer stack is how installs brick relays.
- **`uninstall.sh`**: prints the lines to remove, removes the binaries,
  leaves the state volume with a note on how to delete it.
- README: one-line install front and center; manual steps below; the
  docker.sock risk disclosure; a screenshot of towncrier.
