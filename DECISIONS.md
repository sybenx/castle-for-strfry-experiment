# DECISIONS.md — the graveyard and the waiting room

Decisions made during design and the mid-build re-scope, recorded so they
are not relitigated by accident. Claude Code: append here whenever you make
a call CLAUDE.md doesn't cover. Format: decision, one-line why.

## The re-scope (after Phase 3a)

The Lord narrowed the project to three functions: the invite tree,
protected retention (follows + tree + elevated), and secret favorites
(wards). Everything below in this section was cut in one pass. Each entry
notes its revert path; none should be rebuilt without an explicit new call.

- **gatekeeper / any write-policy plugin** — with bans, mail, and rate
  limits all cut, the plugin's job list was empty; a component that only
  says "accept" is pure maintenance surface. Retention was always enforced
  at raid time, steward-side. Bonus: strfry's single writePolicy slot stays
  FREE, so any stock spam-filter plugin drops in with zero castle code —
  which retires the plugin-chaining requirement outright (chaining was
  only ever needed because the castle occupied the slot). Revert path: the
  built v0.1.1 gatekeeper lives in git history.
- **Bans, kind-1984 report intake, pardons, and domain bans** — never in
  the stated scope; the raid is the moderation, and write-path abuse
  filtering is a spam plugin's job in the free slot. Cutting them also
  deletes their hardest-won machinery (the zombie-ban source-id dedupe,
  raid-time domain re-enumeration, the kind-0 nip05 sweep,
  pardon-beats-ban ordering) — all real bugs that no longer have a place
  to live. Revert path: git history at Phase 3a; if bans return, the
  dedupe lesson below MUST return with them.
- **Castle Mail (the gift-wrap recipient rule + mail bucket + Vault)** —
  out of scope. Consequence, accepted with eyes open: NIP-59 gift wraps
  are signed by random one-time keys, so ALL DMs — including the Lord's
  own — age out at the raid like stranger events. Private DM storage is a
  dedicated relay's job (HAVEN). Stated on the landing page so nobody is
  surprised.
- **Rate limiting (both token buckets)** — existed only as spam knobs;
  spam filtering is delegated to the free writePolicy slot. steward's own
  HTTP API keeps its per-IP limit (different thing, trivial).
- **React-warding** (liking a note silently wards its author) — clever,
  unasked-for, and it carried the only PUBLIC_RELAYS note-fetching in the
  cycle. Wards are added through the UI like everything else.
- **The scribe / POST /api/archive** (paginated backfill of a member's
  history from public relays) — "follows don't get deleted" means don't
  delete what's here, not fetch what isn't. Archival-of-everything is the
  Chronicle relay's job.
- **Byte accounting, size estimates, the neglect nudge, and the
  close-the-gates budget backstop (OUTER_BUDGET_MB / gates.json)** — all
  machinery for a problem the Lord doesn't have: notes are tiny, he checks
  DB size by hand more often than it matters. The raid preview reports an
  event count; the human decides. If DB pressure ever becomes real,
  revisit — the cut designs are in the repo history under the
  SPEC-CHANGE files' final commit.
- **NIP-05 serving** — flavor, not function.

## Rejected earlier (still binding)

- **Building our own relay instead of attaching to strfry** — the castle's
  value is policy, not storage. strfry does the hard part better than a
  rewrite ever would, and the sidecar shape is what lets any existing
  strfry operator adopt the castle without migrating data.
- **Guests tier / thread-context promotion / protected_events.json** —
  required storing event ids as protection targets (violates the state
  invariant). Thread context is Chronicle's job.
- **Feature flags on core behavior (FETCH_CONTEXT, PARDON_BACKFILL, soft
  bytecheck, sync/paginate hybrids)** — two code paths for one behavior;
  choose a behavior instead.
- **Favorite reparenting** — obsolete once elevation became
  tree-independent. Nothing to rescue.
- **Towncrier as a feed (rendering members' notes)** — turns the page into
  a Nostr client; kills the 60KB budget. Rows link to njump instead.
- **install.sh editing strfry.conf / compose / proxy configs** —
  auto-editing unknown Umbrel/Portainer stacks bricks relays. Print, never
  edit.
- **steward holding the Lord's secret key, for any feature** — the
  container is internet-facing and root-equivalent via docker.sock; no
  feature justifies it.
- **Data-triggered automatic raids** — anyone who can inflate the DB would
  control the retention clock for everyone's outer-lands data. Deletion
  only ever happens by human decision.

## Deferred (real ideas, wrong day)

- **One-click self-update** (Lord-only /api/update → helper container runs
  compose pull && up -d). Most delicate code in the project — ship the
  update banner first; add this as its own late phase, disabled by default
  in its first release.
- **Ward viewer permissions** (a ward_viewers pubkey list on
  GET /api/wards). Trivial whenever a concrete need appears — NIP-98
  already authenticates every request. Until then, every viewer is a leak
  surface.
- **docker-socket-proxy hardening** (tecnativa/docker-socket-proxy limited
  to exec on the strfry container, replacing the raw socket mount).
  Documented in the README as the hardening option; make it the default if
  the project grows an audience.
- **Bans / write-path policy, if ever wanted again** — return as a
  separate phase resurrecting gatekeeper from git history, and re-read the
  re-scope section first: report intake without per-report ledger
  source-id dedupe re-bans pardoned pubkeys forever (reports are immortal
  on relays — the zombie-ban bug).

## Decided (calls CLAUDE.md didn't make, still live)

- **The door is outsourced; the government isn't** — the standing rule for
  "could a plugin/external tool do this?" questions. Per-event judgment on
  strangers belongs to third-party plugins in strfry's free writePolicy
  slot; anything touching state, retention, standing, or the tree lives in
  castle. External binaries only through the interfaced-wrapper pattern,
  and only when their availability on real deployments is verified —
  a capability upstream ships but operators commonly disable (e.g.
  negentropy sync) does not count as available.
- **bytecheck is strict from day one; phasing lives in CI wiring, not
  Makefile logic** — a "not yet built, exit 0" soft mode is a conditional
  that outlives its purpose. One behavior: missing file = fail, >60KB =
  fail. Wired into CI in Phase 6a.
- **Every ledger line carries `"v":1`** — ledger.jsonl is the durable
  source of truth; one version field turns a future format change into a
  migration instead of a replay break. Replay fails loudly on unknown
  versions AND, post-demolition, on the removed verbs (dev ledgers with
  old verbs are deleted, not migrated).
- **"Wild West" is renamed "the Outer Lands"** — one concept, one name
  (env var OUTER_TTL_DAYS), folded in before code existed.
- **/api/elevate SETS the requested visibility; only changes are ledgered
  as flip-visibility** — blind toggling meant the Lord re-favoriting
  someone could silently demote a public star into a ward.
- **Kind-0 name cache covers tree members ∪ public favorites ∪
  evicted-in-grace; /api/tree grows a `favored` array** — the public page
  needs names for the Favored and Evicted sections. Still never wards.
- **Manual raids take a per-raid TTL override; eviction grace never
  follows it** — the raid button gets a "days to protect" input (default
  OUTER_TTL_DAYS, min 1; cron raids always use the default; ledger records
  the value used). OUTER_TTL_DAYS protects two unrelated groups — old
  stranger events and recently-evicted members — and the override is aimed
  only at the first: dropping it to purge a spam wave must not wipe
  someone evicted yesterday, so grace stays pinned to the standing
  default.
- **Dry-run IS the raid preview** — {dry_run: true} runs the full scan,
  deletes nothing, returns the event count. Click-triggered, never
  slider-triggered. A per-day age histogram for instant estimates was
  considered and dropped; build it only if preview latency ever annoys.
- **`strfry delete` is confined to one wrapper, ONE call site** (raid.go —
  was two before bans were cut). The only irreversible operation gets one
  choke point where dry-run, batching, and audit logging live.
- **Raid results are "events purged," never disk shrink** — LMDB never
  shrinks (deleted pages are reused; the file stays at high-water mark),
  so `du` on the DB is monotonic. File size is an informational footnote,
  never a signal.
- **Re-elevating to the same visibility appends nothing to the ledger**
  (`State.Elevate` returns `ErrNoChange`; the API treats it as 200). The
  ledger records actual events, not API calls that touched nothing.
- **Outer Lands totals come from one full streaming `strfry scan`, not
  count-filter subtraction** — verified against upstream: `strfry scan` has
  no `--count` flag and no "author NOT IN" filter, so "events belonging to
  no citizen" can't be expressed as a positive filter at all. stats.go's
  `Count` gets its number by streaming `scan`'s NDJSON output and counting
  lines (never parsing/slurping them) for the_lord/citizens, which CAN be
  expressed as positive author filters; outer-lands classification instead
  streams the whole table once and checks each author client-side against
  the citizen set — the same pattern CLAUDE.md's raid pseudocode already
  uses ("stream, don't slurp").
- **`version.running` comes from `-ldflags -X main.buildVersion=...`**,
  set by `git describe --tags` in the Makefile; an unflagged `go build`
  gets `"dev"`. Phase 7's release workflow will pin exact tags at build
  time; nothing else needed to change.
- **The GitHub release check caches its result in `release-check.json`
  with a 24h staleness threshold** — "once a day" (CLAUDE.md) means once a
  day regardless of CYCLE_MINUTES, so a 10-minute cycle doesn't hit GitHub
  144 times a day. A failed refresh keeps the last cached tag rather than
  blanking the update banner.
- **The kind-0 name cache is fully rebuilt from the CURRENT subject set
  every cycle** (tree ∪ public favorites ∪ evicted-in-grace) rather than
  merged with the old file — anyone no longer a subject (lowered, evicted
  past grace, removed from the tree) is dropped immediately. This is also
  what makes ward-absence structural rather than a filter that could be
  forgotten: a ward is simply never in the subject list a fetch is run
  against.
- **Every `docker exec <container> strfry ...` call uses the absolute path
  `/app/strfry`, not bare `strfry`** — verified live against the reference
  `dockurr/strfry` image (the one deploy/docker-compose.yml and
  deploy/smoke.sh actually boot): its strfry binary is not on `$PATH`, so
  the bare command name fails with "executable file not found" via `docker
  exec`. This also fixed a latent bug in raid.go's `dockerStrfryCLI` (built
  ahead of Phase 4, never yet run against a real strfry), caught only
  because Phase 3b's smoke test is the first to actually shell into the
  live scratch container.
- **The delete wrapper's filter carries BOTH `authors` and `until`, not
  authors alone** — a targeted stranger may have posted again after the
  cutoff, and "younger than cutoff" is a keep condition per-event, not
  per-author; an authors-only filter would sweep up their fresh notes too.
  Verified live against `dockurr/strfry`: `strfry delete --filter
  '{"authors":[...], "until": N}'` deletes only that author's events at or
  before N ("Deleting 1 events" in its log), sparing the same author's
  newer note. The raid's scan uses the same discipline: `strfry scan
  '{"until": cutoff}'` (CLAUDE.md's exact pseudocode), also verified live,
  rather than `ScanAll` plus client-side filtering — so a large Outer Lands
  doesn't get slurped on every raid just to find the old slice of it.
- **Dry-run raids ARE appended to the ledger** (`raid-run` with
  `dry_run:true`), for audit — but stats.json's `raids.last_at`/
  `last_purged` are computed only from non-dry-run entries. Reasoning: with
  `RAID_DRY_RUN=true` (the default), every raid is effectively a dry run,
  and the Lord should see "no raid has actually happened yet," not a
  preview's numbers masquerading as a real purge.
- **`RAID_CRON` is a standard 5-field cron expression**, parsed with
  `github.com/robfig/cron/v3` (`cron.ParseStandard`). Chosen over a
  hand-rolled parser: computing `stats.json`'s `raids.next` needs a real
  "next occurrence after now" calculation, which is exactly what the
  library exists for, and it's one well-known dependency with no
  transitive deps of its own — less code than reimplementing schedule math.
  Scheduled raids run on the library's own internal ticker (not tied to
  CYCLE_MINUTES) so they can't drift or be missed by an unrelated loop;
  they always use the default OUTER_TTL_DAYS (no override) and still honor
  RAID_DRY_RUN like any other raid.
- **"CORS: same-origin only" means emitting zero `Access-Control-Allow-*`
  headers, not an allowlist.** The mutation endpoints require both a JSON
  body and a custom `Authorization` header, which forces any cross-origin
  fetch/XHR into a CORS preflight; with no CORS headers at all, the browser
  refuses it itself. The correct amount of CORS code for "same-origin
  only" is zero — adding headers here would only widen the policy that not
  writing them already enforces.
- **NIP-98's `u` tag is matched against `scheme://host+path` reconstructed
  from the request, trusting `X-Forwarded-Proto` when present** — steward
  sits behind a reverse proxy that terminates TLS (CLAUDE.md's "Reverse
  proxy" section), so the connection steward sees is always plain HTTP;
  without trusting the forwarded-proto header, every signed request's `u`
  tag (correctly `https://...`, since that's the real public URL) would
  fail to match. The query string is deliberately excluded from the
  comparison — none of the API's endpoints take one, so this can't be
  used to smuggle unsigned parameters past the check.
- **NIP-98 auth also binds the signature to the request body**, via the
  spec's standard `payload` tag (sha256 hex of the exact bytes sent) —
  required whenever a request carries a non-empty body, checked in
  `authenticate()` (api.go) against the actual bytes read off `r.Body`.
  CLAUDE.md's NIP-98 checklist doesn't mention it, but building towncrier's
  Phase 6b raid control (Preview → confirm → POST /api/raid twice, same
  URL/method, different `dry_run`) surfaced why it's load-bearing: without
  it, the signature only proves "this pubkey hit this URL with this
  method," never which body, so two legitimate requests signed in the same
  wall-clock second are byte-identical events and the replay guard rejects
  the second as a replay of the first. It also closes a real
  confused-deputy gap — a captured `Authorization` header could otherwise
  be resubmitted with an attacker-chosen body (a different invite target,
  `dry_run` flipped, etc.) before the replay guard consumes it. towncrier's
  `nip98Fetch` computes and sends the same hash via `crypto.subtle.digest`.
- **The API's rate limit is a fixed-window 60 requests/minute per IP**
  (steward's `withRateLimit`, `/api/*` only) — CLAUDE.md requires a limit
  but doesn't set a number. Generous enough for a Lord clicking through
  towncrier's UI (each click signs and fires one request), tight enough to
  blunt a script hammering the signed endpoints. A fixed window over a
  sliding one: less code, and the abuse case here is "someone finds the
  API and pounds it," not a precision traffic-shaping problem.
- **Every mutation endpoint's success body is `{"ok":true,"changed":bool}`**
  (invite/remove/ennoble/elevate/lower) — CLAUDE.md doesn't specify a
  response shape for these. `changed` surfaces the true-no-op case
  (re-elevating the same visibility, lowering someone not elevated)
  without the caller needing to diff state before and after the call.
- **`/api/tree`'s JSON shape** (`{owner, members[], favored[], evicted[]}`,
  each entry carrying the kind-0 name/picture already resolved) is this
  phase's concrete answer to CLAUDE.md's prose description, written down
  here so Phase 6a's towncrier can be built against a stable contract
  rather than reverse-engineering api.go.
- **Every ledger-mutating handler and the API's raid trigger share one
  mutex (`Server.mu`)**, and `RAID_CRON`'s scheduled firing (main.go) takes
  the same lock before calling `Cycle.Raid` — ledger.jsonl's
  read-then-append pattern (read, build state, mutate, append) is not
  safe under concurrent writers, and a cron firing during an API mutation
  (or vice versa) is a real scenario once both exist. One lock, held only
  for the read-modify-write section, not the whole request.
- **`stats.json`'s `outer_lands` gained a `ttl_days` field** (the standing
  `OUTER_TTL_DAYS`), added in Phase 6b — CLAUDE.md's raid control spec
  ("days input pre-filled with OUTER_TTL_DAYS") needs the frontend to know
  that value, and nothing in the schema as originally written exposed it.
  Not a secret: it's the standing default TTL, already implied publicly by
  the eviction grace window shown in the Evicted section. Less code than
  a second public endpoint just to carry one integer.

- **The published image's base is `docker:cli`, not `scratch`/`alpine`.**
  steward doesn't talk to a Docker SDK — raid.go and stats.go shell out to
  `docker exec <STRFRY_CONTAINER> strfry ...` as a subprocess — so the
  `docker.sock` mount CLAUDE.md documents is only useful if a `docker`
  client binary exists inside the container to speak to it. `docker:cli`
  ships exactly that client and nothing else (no dockerd), which is less
  code than installing the CLI onto a generic base image by hand.
- **`install.sh` downloads and sha256-verifies the release tarball for
  the host's arch even though the actual deployed artifact is the
  ghcr.io image**, not that tarball. Two things this buys, cheaply: (1) a
  concrete proof the resolved release tag actually exists and is intact
  before any local state changes (volume create, `.env` write); (2) the
  verified tag becomes the version pinned in the printed compose
  snippet (`$IMAGE:$VERSION`, never `:latest`), so a fresh install isn't
  silently exposed to whatever `latest` becomes later. The tarball itself
  is discarded after the check — it's proof-of-integrity, not a runtime.
- **The release workflow calls `docker buildx` directly with
  `tonistiigi/binfmt --install all` for cross-arch QEMU registration**,
  rather than `docker/setup-qemu-action` + `docker/setup-buildx-action`.
  Same mechanism (that image is what those actions wrap), one fewer
  third-party Action to trust for a two-line `docker run`.
- **`install.sh`/`uninstall.sh` prompt via `/dev/tty`, never stdin.** Both
  are meant to run as `curl -fsSL ... | bash`, which leaves stdin attached
  to curl's pipe — reading prompts from stdin would silently consume
  garbage or hang. Reading from the controlling terminal instead keeps the
  one-line curl-pipe install honest with itself; a script with no
  controlling terminal at all (fully non-interactive CI) fails loudly
  instead of hanging.
- **Re-running `install.sh` never overwrites an `.env` that already has
  `OWNER_PUBKEY` set.** "Idempotent, re-runnable" (CLAUDE.md) meant
  picking a rule for the one field a re-run could clobber: an operator
  who hand-edited `RAID_CRON` or `PUBLIC_RELAYS` after the first install
  should be able to re-run the installer (e.g. to re-print the compose
  snippet after losing it) without those edits vanishing. Deleting `.env`
  is the explicit way to force full reconfiguration.
- **`uninstall.sh` removes the locally pulled `ghcr.io/sybenx/castle-steward`
  image and, on confirmation, `.env`; it never touches the `castle-state`
  volume.** The volume holds `ledger.jsonl` — per CLAUDE.md, the only
  durable source of truth for the invite tree and elevations, since Nostr
  events themselves age off relays. An uninstaller that deletes the one
  irreplaceable piece of state by default would violate the load-bearing
  premise the whole ledger design rests on.
- **Split-domain mode (`RELAY_URL`) delivers the relay's address to
  towncrier via `GET /api/config`, not by templating it into
  `index.html`.** towncrier is a pure static file with no build step
  (CLAUDE.md's design creed); injecting a value at deploy time would mean
  either a template step (a build step, which the spec rules out) or
  string-rewriting the shipped file in place (fragile, and it would need
  its own re-run-safety story on every steward restart/redeploy). One
  small public JSON endpoint, fetched once at load and falling back to the
  pre-RELAY_URL window.location derivation on any failure, keeps
  `index.html` byte-identical across same-origin and split-domain
  deployments and keeps `make bytecheck` meaningful (it checks the file
  that ships, not a post-templated one).
- **Same-origin stays the default; `RELAY_URL` is opt-in, unset =
  unchanged behavior.** The premise "open the relay's own URL, see the
  castle" only holds when towncrier is served from that same URL —
  split-domain mode breaks it structurally (a browser hitting the relay's
  hostname gets strfry's stock page, not towncrier; see README, "Split-domain
  deployment"). Existing deploys must not change behavior on upgrade, so
  the empty-`RELAY_URL` path is exactly the code that ran before this
  feature existed, not a special case of a more general one.
- **Audited NIP-98's `u`-tag validation for the split-domain change; no
  fix needed.** `requestURL()` (api.go) reconstructs the URL to compare
  against entirely from the incoming request's own `r.Host` (plus
  `X-Forwarded-Proto`) and `r.URL.Path` — it never references RELAY_URL or
  any assumed relay origin. towncrier's `nip98Fetch` signs the `u` tag as
  `location.origin + path`, i.e. wherever the page itself was loaded from
  (steward's origin in both modes, since towncrier is only ever served by
  steward). The two already agree by construction on both same-origin and
  split-domain deployments; RELAY_URL never entered the auth path in the
  first place, so there was nothing to change.

- **`version.status` is steward's derived update signal** —
  `"update" | "ahead" | "unknown" | "current"` (or absent when no latest is
  known), computed in ONE tested Go function (`versionStatus`, stats.go)
  from the raw running/latest strings; towncrier renders it verbatim and
  never compares tags itself. Semantics chosen: numeric cores decide
  (optional v-prefix, so `0.6.0` == `v0.6.0`); equal cores with a
  git-describe suffix (`-N-gHASH`/`-dirty`) = "ahead" (commits past the
  tag); unparseable running (`dev`, a bare `--always` hash from a shallow
  clone) or unparseable latest = "unknown", rendered as a neutral
  "running X; latest release is Y" — NEVER as "ahead" (the first draft's
  client-side regex claimed dev builds were ahead of releases they could
  be months behind) and never as an update nag (deliberate dev builds
  would be nagged forever). Equal cores with exact running vs a suffixed
  latest (`v1.0.0` vs rc-style `v1.0.0-rc1`) = "current": never prompt a
  downgrade. The update banner embeds the actual one-line command
  (install.sh's curl-pipe, idempotent per the README) instead of pointing
  at the README, satisfying CLAUDE.md's "banner with the one-line update
  command" verbatim.

## The Census re-scope (2026-07-14)

The Lord chose to formally reopen a slice of the rejected surface — a
public DB-transparency layer ("the census") plus a live per-pubkey event
viewer — after weighing the ward-privacy analysis. The deciding principle,
now load-bearing in CLAUDE.md ("The Census"):

- **Public output may show only what anonymous relay queries already
  reveal.** strfry has no read gating; authors, kinds, counts, and
  timestamps are all derivable by any client, so publishing them leaks
  nothing new. Steward-private state (wards above all) never classifies
  census output — the only split used is the public citizen set, under
  which a ward looks like any stranger. This deliberately extends the
  existing "ward privacy is obscurity" trade-off: the census does part of
  an observer's enumeration work, and that is accepted with eyes open
  because the observer could always have done it.
- **The event viewer partially reopens "towncrier as a feed"** — scoped
  to raw escaped text of one author's stored events via a client-side
  NIP-01 REQ (no steward involvement, no auth: the relay answers anyone).
  The original rejection's byte-budget argument didn't survive contact
  (~4KB of JS); the "becomes a Nostr client" argument still binds at the
  boundary: no media, no threads, no reactions, no composer, ever.
- **The census rides the existing cycle scan** — per-author aggregation
  happens inside the same ScanAll pass that already counts the Outer
  Lands, written to census.json (derived, regenerable), served by public
  GET /api/census. Accepted memory shape: ~100 bytes per distinct author
  during the scan; tens of MB only under a 100k-author spam wave. No cap
  machinery until that's real.
- **Still rejected, reaffirmed now:** curated per-user feeds, spam/quality
  judgment (the raid is the moderation), byte/storage accounting,
  time-series/history/heatmaps (point-in-time snapshots only), name
  resolution for census strangers (the kind-0 cache's subject list is
  unchanged), and any sender attribution for encrypted events (NIP-59
  one-time keys make it impossible; counts are aggregate only).

## Accepted trade-offs (known, intentional)

- **docker.sock mount is root-equivalent** on the host from an
  internet-facing container. Accepted for one-DB-owner simplicity;
  disclosed in README; the socket proxy is the mitigation path.
- **DMs are ephemeral on this relay** — see the Castle Mail cut above.
  The castle cannot even see whose DMs they are; that is the protocol
  working as designed.
- **Ward privacy is obscurity, not cryptography.** Whim-timed raids give
  no clean TTL to fingerprint retention against. The threat model that
  defeats this has better attacks available.
- **All admin actions require a browser with NIP-07** (on mobile: an
  Amber-style signer). One auth path, no sessions, no CLI admin surface.
- **Public towncrier data is published by choice** — tree lineage, the
  evicted list, grace expiries. A statement of intent, not a privacy
  proof; wards excepted absolutely.
