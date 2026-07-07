# The Castle — a Pyramid-style tiered relay system for strfry

## What this is

A sidecar + write-policy plugin + interactive web UI for an existing strfry
relay running in Docker (Portainer, on Umbrel). It turns a fully open relay
into a "castle": permanent storage for the owner and an invite-tree of trusted
members (like fiatjaf's Pyramid relay), ephemeral storage for the open wild
west, admin bans driven by the owner's signed Nostr events, and a public,
sign-in-able status page.

**Design creed: light as a whip.** strfry and Pyramid are loved because they
are minimal. Every component here must honor that: no frameworks, no build
steps for the frontend, no databases (flat files + append-only ledger), no
feature that isn't in this spec. When in doubt, leave it out.

Nothing modifies strfry itself. strfry stays stock. Three components:

1. **`gatekeeper`** — strfry write-policy plugin (Go, static binary, stdlib
   only). O(1) accept/reject per event from in-memory hashsets. No network.
2. **`steward`** — sidecar daemon (Go, one binary). Nostr sync, ban intake,
   raids, the invite tree, a small signed-request HTTP API, stats. Serves
   towncrier's static files too, so there is no separate web container.
3. **`towncrier`** — ONE static `index.html` (vanilla JS + CSS, < ~60KB total,
   no dependencies, no bundler). Public castle status + NIP-07 sign-in for
   members.

## The tier system (core domain logic)

Every event falls into exactly one tier. First match wins, top to bottom:

| Tier | Who/what | Write | Retention |
|---|---|---|---|
| **Outlaws** | pubkey in ban set, or NIP-05 claim/enumeration of a banned domain | REJECT | existing events purged |
| **The Lord** | `OWNER_PUBKEY` | accept | permanent |
| **Castle Mail** | kind 1059 (gift wrap) or 9735 (zap receipt) with a `p` tag of the Lord or any Citizen | accept | permanent |
| **Citizens** | invite-tree members ∪ the Lord's kind-3 follows | accept | permanent while citizen |
| **Guests** | events in threads the Lord interacted with (promoted retroactively) | accept | permanent once promoted |
| **Wild West** | everyone else | accept (rate-limited) | pruned after `COURTYARD_TTL_DAYS` (default 30) |

Load-bearing subtleties — do not lose these:

- **Castle Mail is judged by recipient, not author.** NIP-59 gift wraps are
  signed by random one-time keys; author-based rules are blind to them. Match
  kind ∈ {1059, 9735} AND any `p` tag ∈ (Lord ∪ Citizens). This rule precedes
  author rules, is exempt from pruning and from the rate limiter. Without it
  the owner's own DMs get rejected or purged.
- **Ban check precedes everything.** steward must refuse to ever ban
  OWNER_PUBKEY.
- **Losing citizenship is not a purge.** When someone leaves the tree (exiled
  or their branch was cut), their events simply become Wild West and age out
  at the next raid. No special deletion path. (Explicit bans DO purge
  immediately — that's the difference between exile and outlawry.)
- Ephemeral kinds (20000–29999) pass through per NIP-16; ignore in stats.

## The invite tree (Pyramid mechanics)

- A tree rooted at the Lord, persisted as `tree.json`:
  `{"members": {"<pubkey>": {"invited_by": "<pubkey>", "invited_at": ts, "label": "optional petname"}}}`.
  The Lord is the implicit root (not stored as a member).
- **Any tree member may invite**, up to `MAX_INVITES` (default 5) direct
  invitees. `MAX_DEPTH` (default 4) levels below the Lord. Config via env.
- **Removal = cutting a branch.** The Lord may remove any member; a member may
  remove only their own invitees. Removal deletes the node AND its entire
  subtree from the tree. All removals/additions are appended to the ledger.
- **Follows are citizens but not tree members.** They're synced from kind 3
  and cannot invite. The Lord can "ennoble" a follow via the UI, which adds
  them to the tree as his direct invitee (now they can invite). If the Lord
  unfollows an ennobled member, tree membership persists (tree is
  authoritative once ennobled).
- Banning a tree member (report/UI) also cuts their branch.
- Effective citizens = {Lord} ∪ tree members ∪ current follows. steward
  recomputes and writes `citizens.json` every cycle and immediately after any
  API mutation.

## Component 1: gatekeeper (write-policy plugin)

- Go, CGO_ENABLED=0, stdlib + encoding/json only. Runs inside the strfry
  container via strfry.conf `writePolicy.plugin = "/plugin/gatekeeper"`.
- strfry plugin protocol: JSONL on stdin
  (`{"type":"new","event":{...},"sourceInfo":"<ip>"}`), respond
  `{"id":"<event id>","action":"accept"|"reject","msg":"..."}` on stdout,
  flush per line. NOTHING else on stdout; diagnostics to stderr. A malformed
  input line must not kill the loop.
- Reads `banned.json` and `citizens.json` from the shared volume; stats mtime
  ≤ 1/sec; hot-reloads into hashsets. Missing files = empty sets (fail open).
- Per-IP token bucket for non-citizen, non-Castle-Mail events: default
  30 events/min, burst 60.
- Themed reject messages: banned → `blocked: you have been exiled from these
  lands`; rate limit → `rate-limited: the courtyard is busy`.

## Component 2: steward (sidecar daemon)

Own container in the same compose stack. Needs: shared state volume, outbound
network, and `strfry` CLI access via `docker exec` into the strfry container
(mount `/var/run/docker.sock`; prefer this over sharing the LMDB volume — one
DB owner).

Env config: `OWNER_PUBKEY` (hex), `STRFRY_CONTAINER`, `PUBLIC_RELAYS`
(comma-sep), `COURTYARD_TTL_DAYS=30`, `CYCLE_MINUTES=10`,
`RAID_CRON="0 3 * * *"`, `RAID_DRY_RUN=true` (default ON for first deploy),
`MAX_INVITES=5`, `MAX_DEPTH=4`, `PARDON_LIST_DTAG=castle-pardons`,
`FETCH_CONTEXT=false`, `PARDON_BACKFILL=false`, `NIP86_ENABLED=false`,
`LISTEN=:8787`.

### Cycle loop (every CYCLE_MINUTES)

1. **Follows sync.** Fetch owner's kind 3 (own relay + PUBLIC_RELAYS, newest
   wins). On fetch failure keep previous — never shrink on error.
2. **Ban intake.** Fetch kind 1984 authored by OWNER_PUBKEY. p-tags with
   report type `spam`, `illegal`, or `malware` → ban pubkey. Content line
   `ban-domain: <domain>` → ban domain. Other report types ignored (owner may
   report for client-side reasons). Kind-5 deletions by owner e-tagging a
   report void it.
3. **Domain enumeration.** For each banned domain: GET
   `https://<domain>/.well-known/nostr.json` (5s timeout, errors ignored),
   ban every listed pubkey. Also incremental scan of local kind-0 events for
   `nip05` claims of banned domains; full rescan at raid time.
4. **Pardons.** Kind 30000 d=`castle-pardons` by owner. Pardon beats ban,
   always. OWNER_PUBKEY implicitly pardoned.
5. **Ledger & merge.** `ledger.jsonl` is append-only: every ban/unban/pardon/
   invite/remove with source (event id or API request id) and timestamp — the
   durable source of truth (events age off relays; never rebuild state purely
   from live queries). Effective sets = replay(ledger) + follows + pardons.
   Write `banned.json`, `citizens.json`, `tree.json` atomically
   (temp + rename).
6. **Purge newly banned.** `strfry delete --filter '{"authors":[...]}'`,
   ≤50 authors per call.
7. **Promotion.** Fetch the Lord's recent events from own relay; walk e-tags;
   add referenced ids + thread roots + their authors to
   `protected_events.json`. If `FETCH_CONTEXT`, fetch missing thread events
   from PUBLIC_RELAYS and `strfry import`.
8. **Stats.** Write `stats.json` (schema below).

### The Raid (RAID_CRON)

```
cutoff = now - COURTYARD_TTL_DAYS
strfry scan '{"until": cutoff}'   # stream, don't slurp
  keep events where author ∉ citizens
              AND author ∉ protected_authors
              AND id ∉ protected_events
              AND NOT (kind ∈ {1059,9735} AND any p-tag ∈ citizens)
  → strfry delete by id, batches of 1000
```

(strfry delete filters can't express negation; scan-then-delete is required.)
Honor `RAID_DRY_RUN`. Log purge counts to ledger for stats.

### HTTP API (the Pyramid part)

steward serves on `LISTEN`: static towncrier files at `/`, JSON API at `/api`.
Auth: **NIP-98** — `Authorization: Nostr <base64 kind-27235 event>` signed in
the browser via NIP-07. Verify signature, `u` tag matches full URL, `method`
tag matches, created_at within ±60s. Replay-guard: remember event ids for
5 minutes. Identity = event pubkey. No sessions, no cookies, no accounts.

- `GET  /api/stats` — public. stats.json.
- `GET  /api/tree` — public. The invite tree with kind-0 names/pictures
  resolved by steward (cached, refreshed lazily). Pyramid shows its tree
  publicly; so do we.
- `POST /api/invite {pubkey, label?}` — tree members + Lord. Enforce
  MAX_INVITES / MAX_DEPTH / not-banned (banned targets require pardon first).
  Accept npub or hex.
- `POST /api/remove {pubkey}` — Lord: anyone; member: own invitees only.
  Cuts the subtree.
- `POST /api/ennoble {pubkey}` — Lord only. Roots a follow (or anyone) as the
  Lord's direct invitee.
- `POST /api/ban {pubkey|domain}` / `POST /api/pardon {pubkey}` — Lord only.
  Same ledger path as report-driven bans, so all intakes stay consistent.

Every mutation: append to ledger, rewrite state files immediately (no waiting
for next cycle), gatekeeper hot-reloads within a second. Rate-limit the API
per IP. CORS: same-origin only.

### NIP-86 shim (optional, `NIP86_ENABLED`)

Same port, routed by `Content-Type: application/nostr+json+rpc`. Methods:
`banpubkey`, `allowpubkey` (→ pardon), `listbannedpubkeys`,
`listallowedpubkeys` (→ citizens), `banevent` (→ strfry delete by id).
NIP-98 auth, OWNER_PUBKEY only. Writes through the ledger. Lets standard
relay-admin GUIs manage the castle.

## Component 3: towncrier (the page)

ONE `index.html`. Vanilla JS, hand-written CSS, dark castle aesthetic, small
inline SVG castle. No framework, no fonts loaded from CDNs, no analytics,
no build. Target < 60KB total. Everything public on the page is information
already public via CLI queries — the page is honesty, not exposure.

Public view:
- **The Lord** — linked npub, resolved name/avatar.
- **The Court** — the invite tree, rendered as nested lists (`<details>`
  elements — free collapse/expand, zero JS needed), avatars + names, "invited
  by" lineage visible. This is the Pyramid centerpiece.
- **The Citizenry** — count (follows + tree), events stored.
- **The Vault** — castle mail count.
- **Honored Guests** — protected threads + events.
- **The Wild West** — event count, oldest event age, and a live countdown:
  "next raid in 6h 12m". Last raid's purge count.
- **The Exiled** — banned pubkey count, banned domains listed by name.
- Footer: relay URL (click to copy), NIP-11 fields
  (`fetch(origin, {headers:{Accept:'application/nostr+json'}})`), repo link.

Signed-in view (one "Enter the castle" button → NIP-07):
- Tree member: "Invite" (npub paste field + optional petname; shows remaining
  invites), "Remove" on own invitees (confirm dialog warns the whole branch
  falls).
- The Lord: additionally Remove on anyone, Ennoble on follows, Ban / Pardon
  (with a domain-ban field), and a raid-now button (`POST /api/raid`, Lord
  only — add this endpoint).
- If window.nostr is absent, the button explains NIP-07 and links common
  extensions. Signed-in state is just "we hold a pubkey in a JS variable";
  each action signs a fresh NIP-98 event. No persistence.

`stats.json` schema:

```json
{
  "updated_at": 1730000000,
  "the_lord": {"pubkey": "<hex>", "events": 12345},
  "citizens": {"tree": 47, "follows": 812, "events": 480211},
  "castle_mail": {"events": 4021},
  "guests": {"protected_threads": 96, "events": 1834},
  "wild_west": {"events": 231998, "oldest": 1727400000},
  "outlaws": {"pubkeys": 41, "domains": ["bchnostr.com"], "events_purged_total": 88213},
  "raids": {"next": 1730073600, "last_at": 1729987200, "last_purged": 5410},
  "invites": {"max_per_member": 5, "max_depth": 4}
}
```

Counts via batched `strfry scan --count` during the cycle; cache, exactness
not critical.

## Reverse proxy

Document for both Caddy and nginx (Umbrel setups vary):
- WebSocket upgrades AND `Accept: application/nostr+json` → strfry:7777
- everything else → steward:8787 (which serves towncrier + /api)

## Deployment

`docker-compose.yml` fragment for the existing Portainer stack: `steward`
(new), shared named volume `castle-state` mounted into the strfry container at
`/plugin/`, strfry.conf change `writePolicy { plugin = "/plugin/gatekeeper" }`.
Support amd64 + arm64 (plain GOARCH cross-compile in the Makefile). First
deploy runs with RAID_DRY_RUN=true; the README tells the operator to review a
night of dry-run logs before arming it.

## Repo layout

```
castle/
  CLAUDE.md
  Makefile
  gatekeeper/main.go            # stdin/stdout loop, hashsets, rate limiter
  steward/
    main.go cycle.go raid.go ledger.go tree.go api.go nip86.go nostr.go stats.go
  towncrier/index.html
  deploy/docker-compose.yml deploy/strfry.conf.patch deploy/Caddyfile deploy/nginx.conf
  .env.example
```

steward uses github.com/nbd-wtf/go-nostr. gatekeeper stays stdlib-only.

## Testing checklist

- gatekeeper: banned rejected; citizen accepted; gift wrap p-tagging a citizen
  accepted from unknown author and not rate-limited; stranger rate-limited
  after burst; malformed line survives; hot-reload ≤2s.
- tree: invite respects MAX_INVITES/MAX_DEPTH; member can't remove non-invitee;
  Lord removes anyone; cut branch drops whole subtree; ennobled follow
  persists after unfollow; banning a member cuts their branch; ledger replay
  reconstructs identical tree.
- API: bad NIP-98 sig rejected; stale created_at rejected; replayed event id
  rejected; `u`/`method` mismatch rejected; every mutation appears in ledger
  and state files immediately.
- steward: spam/illegal/malware report bans, nudity report doesn't; kind-5
  unban; pardon beats ban; OWNER_PUBKEY unbannable; follows never shrink on
  fetch error; well-known enumeration; `ban-domain:` convention.
- raid: protected thread survives; citizen + castle mail survive; 31-day
  stranger note deleted; ex-citizen events deleted after branch cut; dry-run
  deletes nothing.
- Smoke: compose up scratch strfry, publish fixtures with nak, assert via
  strfry scan; drive the API with curl + nak-signed NIP-98 headers.

## Hard-won context (do not "simplify" these away)

- NIP-56 warns relays not to auto-moderate on public reports (gameable). This
  design is immune: only reports SIGNED BY OWNER_PUBKEY count. Never widen
  intake without an explicit trust list.
- NIP-59 gift wraps use random one-time signing keys. Author-only whitelists
  silently kill the owner's own DMs. The recipient-based Castle Mail rule is
  load-bearing.
- NIP-05 is self-asserted; domain bans are a convenience against lazy spam
  farms, not a security boundary. Pubkey bans are the backbone.
- There is no protocol "unreport"; undo = kind-5 deletion of the report and/or
  the pardon list. The ledger exists because events age off relays.
- strfry has no read gating. Truly private storage (auth-gated DM reads) is
  delegated to the owner's existing HAVEN relay; the castle stores mail but
  cannot hide its existence. Accepted, and stated on the landing page.
- The invite tree is the accountability mechanism (lineage), not a growth
  mechanism. Resist any feature that weakens "your invitees are your
  responsibility."

## Distribution (first-class deliverables, not afterthoughts)

- **Releases via GitHub Actions**: on tag push, build static `gatekeeper` and
  `steward` binaries for linux/amd64 + linux/arm64 (CGO_ENABLED=0), attach to
  a GitHub Release, and push a multi-arch `steward` image to ghcr.io.
  goreleaser or a plain build matrix — whichever is fewer lines.
- **`install.sh`** (curl-pipe-able, idempotent, re-runnable):
  1. Detect arch and a running strfry container (confirm with the user).
  2. Prompt for OWNER_PUBKEY (accept npub or hex; convert npub locally).
  3. Download release binaries matching the installed version tag; verify
     sha256 from the release checksums file.
  4. Create the `castle-state` volume; place gatekeeper at `/plugin/gatekeeper`.
  5. Back up strfry.conf, then idempotently ensure
     `writePolicy { plugin = "/plugin/gatekeeper" }`.
  6. Write `.env` (RAID_DRY_RUN=true), add the steward service to the compose
     stack (or emit a snippet if the stack file can't be located).
  7. PRINT the reverse-proxy snippet for Caddy and nginx — never edit the
     user's proxy config automatically.
  8. Restart strfry, health-check `/api/stats`, print the towncrier URL.
- **`uninstall.sh`**: remove the writePolicy block (restore backup), remove
  steward from the stack, leave the state volume with a note on how to delete
  it.
- README: one-line install front and center; manual install steps below it;
  a screenshot of towncrier.
