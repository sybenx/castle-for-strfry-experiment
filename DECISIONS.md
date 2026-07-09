# DECISIONS.md — the graveyard and the waiting room

Decisions made during design, recorded so they are not relitigated by
accident. Claude Code: append here whenever you make a call CLAUDE.md
doesn't cover. Format: date, decision, one-line why.

## Rejected (do not build)

- **Building our own relay instead of attaching to strfry** — the castle's
  value is policy, not storage. strfry already does the hard part (LMDB
  engine, firehose-grade ingest, protocol conformance, sync) better than a
  rewrite ever would, the firehose-by-default outer lands lean on exactly
  that strength, and the sidecar+plugin shape is what lets any existing
  strfry operator adopt the castle without migrating data. The ledger and
  flat state files keep the door open to a different engine someday;
  walking through it is a different project.
- **Guests tier / thread-context promotion / protected_events.json** —
  required storing event ids (violates the state invariant) and duplicated
  the Lord's Chronicle relay. Thread context is Chronicle's job.
- **FETCH_CONTEXT / PARDON_BACKFILL flags** — flags on core behavior are two
  code paths; behaviors were chosen instead (no fetching, no backfill).
- **kind-5 report voids + kind-30000 pardon-list sync** — undo now lives in
  exactly one place: the web UI pardon, through the ledger.
- **Favorite reparenting (cut-branch favorites become the Lord's invitees)** —
  obsolete once elevation became tree-independent. Nothing to rescue.
- **Automatic backfill of all follows/citizens** — a standing network job
  with real bandwidth, disk, and relay-reputation costs. Archival is manual,
  per-member, one-shot (the scribe).
- **Towncrier as a feed (rendering members' notes)** — turns the page into a
  Nostr client; kills the 60KB budget. Rows link to njump instead.
- **Automated diamond-sorting (public reports, follows-of-follows
  whitelists, AI filtering)** — public reports are gameable (NIP-56),
  whitelists kill the open lands, and the raid already IS the moderation:
  spam is outlived, not judged.
- **install.sh editing strfry.conf / compose / proxy configs** — auto-editing
  unknown Umbrel/Portainer stacks bricks relays. Print, never edit.
- **steward holding the Lord's secret key (for DM triage or anything else)**
  — the container is internet-facing and root-equivalent via docker.sock;
  putting the identity key on it turns a bad day into a catastrophe. No
  feature justifies it.

## Deferred (real ideas, wrong day)

- **NIP-86 admin shim** (banpubkey/allowpubkey/... routed by Content-Type,
  OWNER_PUBKEY only, through the ledger). Demand should precede code;
  the API + towncrier cover every operation.
- **One-click self-update** (Lord-only /api/update → helper container runs
  compose pull && up -d; steward image bundles gatekeeper and refreshes
  /plugin/ on boot). Most delicate code in the project — ship the update
  banner first, add this as its own late phase with a disabled-by-default
  first release.
- **Ward viewer permissions** (a ward_viewers pubkey list checked on
  GET /api/wards). Trivial whenever a concrete need appears — NIP-98 already
  authenticates every request. Until then, every viewer is a leak surface.
- **Random citizen note pulls** ("the castle feels alive": one REQ per cycle
  for a random citizen's recent notes). ~30 lines, harmless traffic, but not
  load-bearing. Considered, deferred.
- **Medieval office naming for steward modules** (constable/seneschal/
  bailiff/reeve, from a sibling design fork). Pure theming — the one real
  safety property under it (delete confinement) was adopted on its own.
  Rename modules later if the flavor is wanted; zero behavior change.
- **docker-socket-proxy hardening** (tecnativa/docker-socket-proxy limited
  to exec on the strfry container, replacing the raw socket mount).
  Documented in the README as the hardening option; make it the default if
  the project grows an audience.
- **Lord-login DM triage** (towncrier unwraps Castle Mail client-side via
  NIP-07 nip44 when the Lord signs in; the Lord judges, spam is purged and
  the Lord's public mute list updated so junk vanishes from clients, not
  just this relay). Real idea, wrong day: extension support for unwrapping
  gift wraps is inconsistent, it strains the 60KB budget, and the purge
  would be a THIRD `strfry delete` call site — if ever built it must route
  through the existing wrapper. The mail bucket plus the visible Vault
  count cover v1; demand should precede code.

## Decided (calls CLAUDE.md didn't make)

- **Ephemeral kinds (20000–29999) get stranger treatment — the lands
  bucket, whatever its setting (unlimited at the default)** — a special
  case is a second code path; whatever rule governs stranger posts governs
  ephemeral floods too. Citizens are already exempt. "Pass through per
  NIP-16" means strfry doesn't store them; it says nothing about the write
  path. Mirrored into CLAUDE.md's tier notes (the spec is the source of
  truth; behaviors must not live only here). The gatekeeper-side logic is
  pinned by `TestDecide_EphemeralStrangerRidesBucket`, which must run with
  the lands bucket ENABLED, since at the default it is unlimited. The
  premise this rests on — that strfry actually invokes the write-policy
  plugin for ephemeral-kind events at all — is still UNVERIFIED: the
  Phase 1 session had no Docker available, so the real-strfry confirmation
  PLAN.md calls for did not happen. See `.claude/notes/phase1.md`. Whoever
  gets Docker next must run it before trusting this line, per PLAN.md's own
  instruction to drop the pinning test if the premise turns out false.
- **bytecheck is strict from day one; phasing lives in CI wiring, not
  Makefile logic** — a "not yet built, exit 0" soft mode is a conditional
  that outlives its purpose: after Phase 6a a missing index.html would
  pass green. One behavior (missing file = fail, >60KB = fail), added to
  the CI workflow in Phase 6a.
- **internal/stateformat is born in Phase 1, not retrofitted in 3a** —
  stdlib-only shared types for banned.json/citizens.json; refactoring a
  tagged v0.1.0 component mid-project costs more than starting shared.
- **Report intake is idempotent per report (ledger source-id dedupe)** —
  removing kind-5 voids created the zombie-ban bug: reports are immortal on
  relays, so re-reading the same 1984 every cycle re-banned pardoned
  pubkeys within CYCLE_MINUTES. Each report now bans exactly once, ever;
  domain re-enumeration and the kind-0 sweep skip pardoned pubkeys. Emergent
  semantics, intended: pardon beats everything before it, a NEW report is a
  fresh judgment and re-bans. Caught by a cross-fork review.
- **`strfry delete` is confined to one wrapper, two call sites** (raid.go +
  purge-newly-banned). The only irreversible operation gets one choke point
  where dry-run, batching, and audit logging live. Adopted from the other
  fork's chain-of-command design, minus the office theming.
- **Outer-lands-neglect nudge uses event count and oldest age, never DB file
  size** — LMDB never shrinks (deleted pages are reused, the file stays at
  high-water mark), so `du` on the DB is monotonic and would nudge forever
  after a thorough raid. File size is an informational footnote at most.
- **Every ledger line carries `"v":1`** — ledger.jsonl is the durable
  source of truth; one version field turns a future format change into a
  migration instead of a replay break. Replay fails loudly on unknown
  versions.
- **"Wild West" is renamed "the Outer Lands"** — pure theming, folded in
  before any code exists so there is no migration. The stray "courtyard"
  wording is unified under the same name (env var is now OUTER_TTL_DAYS,
  reject message and neglect nudge reworded): one concept, one name.
- **Two buckets, inverted from the first draft: mail is always throttled,
  the outer lands are a firehose by default** — mail (kind 1059/9735 to the
  Lord or a citizen) is the one lane where a stranger earns PERMANENT
  storage, so its tight bucket (MAIL_RATE_PER_MIN=10, burst 2×) is always
  on; public posts age out at the next raid anyway, and months of running a
  fully open strfry produced only mild spam, so LANDS_RATE_PER_MIN defaults
  to 0 (unlimited) and is an operator knob for larger or spam-prone relays.
  Human DM rates never notice the mail bucket, and gift-wrap sender
  anonymity preserves the appeals path: banned pubkeys can still write the
  Lord, just never at flood speed. SUPERSEDES two Phase 1 calls made
  against a stale copy of the spec: "Castle Mail rides the (single) per-IP
  token bucket" and "rate-limit numbers are hardcoded constants, not
  env-configurable" — both rates are env knobs read by gatekeeper, per the
  final CLAUDE.md. Gatekeeper remediation tracked in
  `.claude/notes/phase1-remediation.md`.
- **/api/elevate SETS the requested visibility; only flip-visibility
  toggles — and react-warding skips OWNER_PUBKEY and the already-elevated**
  — blind toggling meant the Lord liking a favorite's note silently demoted
  their public star into a ward. Caught in spec review.
- **Kind-0 name cache covers tree members ∪ public favorites ∪
  evicted-in-grace; /api/tree grows a `favored` array** — the public page
  renders names in the Favored and Evicted sections, and previously had no
  data source for the favorites list at all (stats.json only carries a
  count). Still never wards.
- **gatekeeper's state directory is hardcoded to `/plugin`, no env var** —
  install.sh already places the gatekeeper binary itself at
  `/plugin/gatekeeper`, so the deploy layout is load-bearing on this exact
  path, and an override knob would be a feature the spec doesn't ask for.
  Tests construct a `*store` directly against a `t.TempDir()` and never go
  through `main()`, so testability doesn't need it either. (The two rate
  env knobs are gatekeeper's entire env surface.)

## Accepted trade-offs (known, intentional)

- **docker.sock mount is root-equivalent** on the host from an
  internet-facing container. Accepted for one-DB-owner simplicity; disclosed
  in README; proxy is the mitigation path.
- **Ward privacy is obscurity, not cryptography.** Whim-timed raids give no
  clean TTL to fingerprint retention against. The threat model that defeats
  this ("someone obsessively measuring one stranger's event lifetimes")
  has better attacks available.
- **Scribe pagination is leaky** (shared timestamps, silent relay caps).
  Best-effort archival by design; no reconciliation engine.
- **Web-first pardons mean unbanning requires a browser with NIP-07** (on
  mobile: an Amber-style signer). Accepted for deleting two sync
  subsystems. Banning still works from any client via kind-1984 reports.
- **Domain bans re-enumerate at raid cadence, not cycle cadence.** A spam
  farm's fresh pubkeys live until the next raid. Acceptable: their events
  die in the same raid that bans them.
- **NIP-46 signer traffic rides the lands bucket** — remote signing uses
  ephemeral non-citizen client keys, so it gets stranger treatment:
  unlimited at the default, throttled only where an operator has set
  LANDS_RATE_PER_MIN. If such a Lord ever hits it, the fix is elevating the
  client key or raising the number, not an exemption code path.
- **Archiving a ward emits a metadata signal** — the scribe sends
  `{"authors":[ward]}` REQs to public relays, announcing the castle's
  interest in that pubkey to third parties. Same obscurity budget as ward
  privacy generally (declared obscurity-not-cryptography); accepted, but
  the Lord should know the scribe is the one place the castle actively
  asks about a ward in public.
- **The invariant permits provenance event ids** — ban sources and the
  follows-snapshot source are stored event ids, deliberately. The forbidden
  thing is event ids as retention/protection TARGETS. Earlier absolute
  wording ("never event ids") read as self-contradicting; reworded in
  CLAUDE.md and PLAN.md.
- **Re-elevating to the same visibility appends nothing to the ledger**
  (`State.Elevate` returns `ErrNoChange`). CLAUDE.md says re-elevating
  "SETS the requested visibility (idempotent; a change is ledgered as
  flip-visibility)" but doesn't say what happens when there's no change.
  Treating a true no-op as "don't write a ledger line" keeps the ledger a
  record of actual events, not of API calls that happened to touch nothing;
  Phase 5a's `/api/elevate` handler should treat `ErrNoChange` as success
  (200), not a client error.
- **`State.Citizens` excludes banned pubkeys defensively**, even though
  CLAUDE.md's formula ({Lord} ∪ tree ∪ follows ∪ elevated) doesn't mention
  bans. Belt-and-suspenders: gatekeeper's Outlaws tier already wins over
  citizenship regardless of citizens.json's contents, but excluding bans
  here stops stats/citizen counts from double-counting an outlaw who's
  still in a stale follows.json snapshot (follows sync is async per Phase
  3a and can't retroactively edit history the moment a ban lands).
- **Ban-cuts-branch grace-periods the subtree, not the banned pubkey
  itself.** `State.Apply`'s `VerbBan` case adds every removed descendant to
  `Evicted` except the banned pubkey (an outlaw, purged immediately per
  CLAUDE.md's exile-vs-outlawry distinction). A plain `Remove` grace-periods
  everyone removed, root included. Two different eviction outcomes from
  structurally the same "cut a branch" operation, keyed on whether the root
  was banned or merely removed — easy to get backwards, pinned by
  `TestBanningTreeMemberCutsBranchAndGracePeriodsSubtreeOnly`.
