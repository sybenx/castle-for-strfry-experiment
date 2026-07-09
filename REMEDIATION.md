# Phase 1 remediation — read before starting Phase 3a

## What happened
Phases 0–2 were built against a STALE copy of the spec (pre-firehose).
The final spec inverted the rate-limit design; gatekeeper currently
implements the rejected version. Phase 2 (ledger/tree/elevation) is
unaffected. Save this file's contents to `.claude/notes/phase1-remediation.md`
and delete it from the repo root when all items below are done.

## Step 0 — replace the spec files
Overwrite the repo's CLAUDE.md, PLAN.md, and DECISIONS.md with the versions
provided alongside this note. They are the source of truth. Do NOT merge by
hand — the provided DECISIONS.md already preserves every entry added during
Phases 0–2, with two Phase 1 calls marked as superseded.

## Step 1 — rework gatekeeper's rate limiting (the superseded calls)
Per the final CLAUDE.md gatekeeper section:
- TWO per-IP token buckets, burst = 2× rate, idle eviction unchanged:
  - **Mail bucket** — ALWAYS on; applies only to Castle Mail (kind
    1059/9735 with a p-tag ∈ Lord ∪ citizens). `MAIL_RATE_PER_MIN`
    (default 10). Reject message: `rate-limited: the lord's courier is
    overburdened`.
  - **Lands bucket** — every other non-citizen event. `LANDS_RATE_PER_MIN`
    (default 0 = DISABLED, full firehose). Reject message unchanged:
    `rate-limited: the outer lands are crowded`.
- Both knobs are env vars read by gatekeeper at startup (this supersedes
  the "hardcoded constants" decision). Add them to .env.example with their
  defaults (LANDS_RATE_PER_MIN=0, MAIL_RATE_PER_MIN=10).
- Update tests per the final CLAUDE.md checklist:
  - stranger firehose passes untouched at the default LANDS_RATE_PER_MIN=0;
  - with the lands bucket set, a stranger is limited after burst;
  - gift wrap p-tagging a citizen is accepted from an unknown author but
    rides the always-on mail bucket (permanence ≠ bucket exemption);
  - `TestDecide_EphemeralStrangerRidesBucket` now runs with the lands
    bucket ENABLED (at the default it is unlimited);
  - citizens remain exempt from both buckets.
- Re-run the FULL gatekeeper fixture suite plus the elevation privacy tests
  (standing order after any tier logic change). Tag v0.1.1.

## Step 2 — pay the Phase 1 Docker debt
The Phase 1 acceptance smoke never ran (no Docker in that session). Before
any Phase 3a work, against a real strfry in docker:
1. citizen event accepted; banned event rejected;
2. CONFIRM strfry routes an ephemeral-kind (20000–29999) event through the
   write policy at all. If it does NOT, drop the ephemeral pinning test and
   record that in DECISIONS.md, per PLAN.md.
Record the outcome in `.claude/notes/phase1.md` (replacing the UNVERIFIED
note) and update the DECISIONS.md ephemeral entry accordingly.

## Step 3 — only then start Phase 3a.
