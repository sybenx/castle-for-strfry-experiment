// Cycle loop: follows sync, report intake, react-warding, ledger merge,
// purge-newly-banned. Runs every CYCLE_MINUTES. Stats (step 6) lands in
// Phase 3b. See CLAUDE.md, "Cycle loop (every CYCLE_MINUTES)".
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Cycle holds everything one run of the cycle loop needs. Fetcher and CLI
// are interfaces precisely so tests can fake the network and strfry
// without either a live relay or a live container.
type Cycle struct {
	StateDir     string
	Owner        string
	OwnRelay     string
	PublicRelays []string
	MaxInvites   int
	MaxDepth     int
	Fetcher      NostrFetcher
	CLI          strfryCLI
	Now          func() time.Time
}

// NewCycle builds a Cycle from config plus the real network/CLI
// dependencies. Used by main.go; tests construct a Cycle literal directly
// with fakes instead.
func NewCycle(cfg config, fetcher NostrFetcher, cli strfryCLI) *Cycle {
	return &Cycle{
		StateDir:     "/state",
		Owner:        cfg.OwnerPubkey,
		OwnRelay:     ownRelayURL(cfg.StrfryContainer),
		PublicRelays: cfg.PublicRelays,
		MaxInvites:   cfg.MaxInvites,
		MaxDepth:     cfg.MaxDepth,
		Fetcher:      fetcher,
		CLI:          cli,
		Now:          time.Now,
	}
}

func (c *Cycle) ledgerPath() string    { return filepath.Join(c.StateDir, "ledger.jsonl") }
func (c *Cycle) followsPath() string   { return filepath.Join(c.StateDir, "follows.json") }
func (c *Cycle) watermarkPath() string { return filepath.Join(c.StateDir, "react_watermark.json") }
func (c *Cycle) bannedPath() string    { return filepath.Join(c.StateDir, "banned.json") }
func (c *Cycle) citizensPath() string  { return filepath.Join(c.StateDir, "citizens.json") }
func (c *Cycle) treePath() string      { return filepath.Join(c.StateDir, "tree.json") }

// Run executes one full cycle: follows sync, report intake, react-warding,
// ledger merge (banned/citizens/tree written atomically), and purging the
// pubkeys newly banned this cycle. Stats generation is Phase 3b.
func (c *Cycle) Run(ctx context.Context) error {
	entries, err := ReadLedger(c.ledgerPath())
	if err != nil {
		return fmt.Errorf("cycle: read ledger: %w", err)
	}
	state, err := BuildState(c.Owner, entries, c.MaxInvites, c.MaxDepth)
	if err != nil {
		return fmt.Errorf("cycle: build state: %w", err)
	}
	seenSources := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Source != "" {
			seenSources[e.Source] = true
		}
	}

	// 1. Follows sync.
	follows, err := c.syncFollows(ctx)
	if err != nil {
		return fmt.Errorf("cycle: sync follows: %w", err)
	}

	var newEntries []Entry

	// 2. Report intake.
	reportEntries, err := c.intakeReports(ctx, state, seenSources)
	if err != nil {
		return fmt.Errorf("cycle: intake reports: %w", err)
	}
	newEntries = append(newEntries, reportEntries...)

	// 3. React-warding.
	wardEntries, err := c.reactWard(ctx, state)
	if err != nil {
		return fmt.Errorf("cycle: react-warding: %w", err)
	}
	newEntries = append(newEntries, wardEntries...)

	// 4. Ledger & merge.
	for _, e := range newEntries {
		if err := AppendLedger(c.ledgerPath(), e); err != nil {
			return fmt.Errorf("cycle: append ledger: %w", err)
		}
	}
	if err := writeJSONAtomic(c.bannedPath(), state.BannedJSON()); err != nil {
		return fmt.Errorf("cycle: write banned.json: %w", err)
	}
	if err := writeJSONAtomic(c.citizensPath(), state.CitizensJSON(follows.Pubkeys)); err != nil {
		return fmt.Errorf("cycle: write citizens.json: %w", err)
	}
	if err := writeJSONAtomic(c.treePath(), state.Tree); err != nil {
		return fmt.Errorf("cycle: write tree.json: %w", err)
	}

	// 5. Purge newly banned. Domain bans are resolved to pubkeys at raid
	// time only (CLAUDE.md), so only this cycle's fresh VerbBan entries
	// are purge targets here.
	var newlyBanned []string
	for _, e := range newEntries {
		if e.Verb == VerbBan {
			newlyBanned = append(newlyBanned, e.Pubkey)
		}
	}
	if len(newlyBanned) > 0 && c.CLI != nil {
		if _, err := c.CLI.DeleteByAuthors(ctx, newlyBanned, false); err != nil {
			fmt.Fprintf(os.Stderr, "steward: purge newly banned: %v\n", err)
		}
	}

	return nil
}

// FollowsSnapshot is follows.json's schema: the Lord's last-good kind-3
// pubkey list plus its source event id and created_at, so a fetch failure
// or a restart mid-outage can never shrink the citizenry (CLAUDE.md,
// "Durable state").
type FollowsSnapshot struct {
	Pubkeys   []string `json:"pubkeys"`
	Source    string   `json:"source"`
	CreatedAt int64    `json:"created_at"`
}

func readFollows(path string) (FollowsSnapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return FollowsSnapshot{}, nil
	}
	if err != nil {
		return FollowsSnapshot{}, err
	}
	var snap FollowsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return FollowsSnapshot{}, err
	}
	return snap, nil
}

// syncFollows fetches the Lord's newest kind-3 across OwnRelay + PublicRelays
// and replaces follows.json only if it is newer than what's on disk. Any
// fetch failure (or no kind-3 found anywhere) keeps the previous snapshot —
// "never shrink on error" — and is logged, never fatal to the cycle.
func (c *Cycle) syncFollows(ctx context.Context) (FollowsSnapshot, error) {
	path := c.followsPath()
	current, err := readFollows(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: read follows.json: %v (keeping empty)\n", err)
		current = FollowsSnapshot{}
	}

	relays := append([]string{c.OwnRelay}, c.PublicRelays...)
	latest, err := c.Fetcher.LatestKind3(ctx, relays, c.Owner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: follows sync failed, keeping previous snapshot: %v\n", err)
		return current, nil
	}
	if latest == nil || int64(latest.CreatedAt) <= current.CreatedAt {
		return current, nil
	}

	var pubkeys []string
	for _, tag := range latest.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			pubkeys = append(pubkeys, tag[1])
		}
	}
	sort.Strings(pubkeys)
	next := FollowsSnapshot{Pubkeys: pubkeys, Source: latest.ID, CreatedAt: int64(latest.CreatedAt)}
	if err := writeJSONAtomic(path, next); err != nil {
		fmt.Fprintf(os.Stderr, "steward: write follows.json: %v (keeping previous)\n", err)
		return current, nil
	}
	return next, nil
}

// isBannableReportType matches CLAUDE.md's report intake: only these three
// NIP-56 report types ban a pubkey; everything else (nudity, etc.) is
// ignored.
func isBannableReportType(t string) bool {
	return t == "spam" || t == "illegal" || t == "malware"
}

// intakeReports fetches kind-1984 reports authored by the Lord and applies
// bans (pubkey or domain), skipping any report whose event id already
// appears as a ledger source — each report bans exactly once, ever, so a
// pardon holds even though the old report is immortal on relays (the
// zombie-ban bug this dedupe exists to prevent).
func (c *Cycle) intakeReports(ctx context.Context, state *State, seenSources map[string]bool) ([]Entry, error) {
	reports, err := c.Fetcher.Reports(ctx, c.OwnRelay, c.Owner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: report fetch failed: %v\n", err)
		return nil, nil
	}

	now := c.Now().Unix()
	var newEntries []Entry
	for _, r := range reports {
		if seenSources[r.ID] {
			continue
		}
		for _, tag := range r.Tags {
			if len(tag) < 3 || tag[0] != "p" || !isBannableReportType(tag[2]) {
				continue
			}
			e, err := state.BanPubkey(tag[1], r.ID, now)
			if err != nil {
				if !errors.Is(err, ErrOwnerUnbannable) {
					fmt.Fprintf(os.Stderr, "steward: report %s ban %s: %v\n", r.ID, tag[1], err)
				}
				continue
			}
			newEntries = append(newEntries, e)
		}
		for _, line := range strings.Split(r.Content, "\n") {
			domain, ok := strings.CutPrefix(strings.TrimSpace(line), "ban-domain:")
			if !ok {
				continue
			}
			domain = strings.TrimSpace(domain)
			if domain == "" {
				continue
			}
			e, err := state.BanDomain(domain, r.ID, now)
			if err != nil {
				fmt.Fprintf(os.Stderr, "steward: report %s ban-domain %s: %v\n", r.ID, domain, err)
				continue
			}
			newEntries = append(newEntries, e)
		}
	}
	return newEntries, nil
}

// reactWatermark is react_watermark.json's schema: the created_at of the
// newest kind-7 reaction processed so far. Not ledger-derived (there is no
// watermark verb) — a small standalone cursor file, same spirit as
// follows.json.
type reactWatermark struct {
	Since int64 `json:"since"`
}

func readWatermark(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var w reactWatermark
	if err := json.Unmarshal(data, &w); err != nil {
		return 0, err
	}
	return w.Since, nil
}

// reactWard fetches the Lord's kind-7 reactions since the watermark, wards
// (elevate public=false) the author of each reacted-to note, and bumps the
// watermark past every reaction seen this cycle regardless of outcome —
// otherwise an unresolvable note would be retried forever.
func (c *Cycle) reactWard(ctx context.Context, state *State) ([]Entry, error) {
	path := c.watermarkPath()
	wm, err := readWatermark(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: read react watermark: %v (starting from 0)\n", err)
		wm = 0
	}

	reactions, err := c.Fetcher.Reactions(ctx, c.OwnRelay, c.Owner, wm+1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: reaction fetch failed: %v\n", err)
		return nil, nil
	}
	if len(reactions) == 0 {
		return nil, nil
	}

	now := c.Now().Unix()
	maxSeen := wm
	var newEntries []Entry
	for _, r := range reactions {
		if int64(r.CreatedAt) > maxSeen {
			maxSeen = int64(r.CreatedAt)
		}

		eTag := r.Tags.Find("e")
		if len(eTag) < 2 {
			continue
		}
		note, err := c.Fetcher.Event(ctx, c.OwnRelay, c.PublicRelays, eTag[1])
		if err != nil || note == nil {
			continue
		}
		author := note.PubKey
		if author == c.Owner || state.Elevation.IsElevated(author) {
			continue
		}
		e, err := state.Elevate(author, false, r.ID, now)
		if err != nil {
			if !errors.Is(err, ErrNoChange) {
				fmt.Fprintf(os.Stderr, "steward: react-ward %s: %v\n", author, err)
			}
			continue
		}
		newEntries = append(newEntries, e)
	}

	if maxSeen > wm {
		if err := writeJSONAtomic(path, reactWatermark{Since: maxSeen}); err != nil {
			fmt.Fprintf(os.Stderr, "steward: write react watermark: %v\n", err)
		}
	}
	return newEntries, nil
}

// strfryCLI is the interface to strfry's CLI, reached via `docker exec`
// into STRFRY_CONTAINER. strfry delete is the only irreversible operation
// in the system; ALL delete calls go through this one wrapper (CLAUDE.md's
// "Delete confinement") — the two permitted call sites are the
// purge-newly-banned step above and raid.go's sweep (Phase 4). Interfaced
// so tests can fake it without a live strfry.
type strfryCLI interface {
	// DeleteByAuthors deletes every event authored by any of pubkeys,
	// batching at most deleteBatchSize per call. If dryRun, it logs the
	// batches it would run and deletes nothing. Returns the number of
	// pubkeys targeted.
	DeleteByAuthors(ctx context.Context, pubkeys []string, dryRun bool) (int, error)
}

const deleteBatchSize = 50

// dockerStrfryCLI is the real strfryCLI, shelling out to
// `docker exec <container> strfry delete --filter ...`.
type dockerStrfryCLI struct {
	Container string
}

func (d *dockerStrfryCLI) DeleteByAuthors(ctx context.Context, pubkeys []string, dryRun bool) (int, error) {
	for _, batch := range chunkStrings(pubkeys, deleteBatchSize) {
		filter, err := json.Marshal(map[string]any{"authors": batch})
		if err != nil {
			return 0, err
		}
		if dryRun {
			fmt.Fprintf(os.Stderr, "steward: [dry-run] would delete %d authors: %s\n", len(batch), filter)
			continue
		}
		cmd := exec.CommandContext(ctx, "docker", "exec", d.Container, "strfry", "delete", "--filter", string(filter))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("strfry delete: %w: %s", err, out)
		}
		fmt.Fprintf(os.Stderr, "steward: deleted %d authors' events\n", len(batch))
	}
	return len(pubkeys), nil
}

func chunkStrings(items []string, size int) [][]string {
	if size <= 0 || len(items) == 0 {
		return nil
	}
	var out [][]string
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[i:end])
	}
	return out
}
