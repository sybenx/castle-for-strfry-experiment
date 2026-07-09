// Cycle loop: follows sync, ledger merge. Runs every CYCLE_MINUTES. Stats
// (step 3) lands in Phase 3b. See CLAUDE.md, "Cycle loop (every
// CYCLE_MINUTES)".
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Cycle holds everything one run of the cycle loop needs. Fetcher, Scanner,
// and ReleaseChecker are interfaces precisely so tests can fake the network
// and strfry without either being live.
type Cycle struct {
	StateDir       string
	Owner          string
	OwnRelay       string
	PublicRelays   []string
	MaxInvites     int
	MaxDepth       int
	OuterTTLDays   int
	RunningVersion string
	Fetcher        NostrFetcher
	Scanner        strfryScanner
	ReleaseChecker ReleaseChecker
	Now            func() time.Time
}

// NewCycle builds a Cycle from config plus the real network/strfry
// dependencies. Used by main.go; tests construct a Cycle literal directly
// with fakes instead.
func NewCycle(cfg config, fetcher NostrFetcher, scanner strfryScanner, releaseChecker ReleaseChecker) *Cycle {
	return &Cycle{
		StateDir:       "/state",
		Owner:          cfg.OwnerPubkey,
		OwnRelay:       ownRelayURL(cfg.StrfryContainer),
		PublicRelays:   cfg.PublicRelays,
		MaxInvites:     cfg.MaxInvites,
		MaxDepth:       cfg.MaxDepth,
		OuterTTLDays:   cfg.OuterTTLDays,
		RunningVersion: buildVersion,
		Fetcher:        fetcher,
		Scanner:        scanner,
		ReleaseChecker: releaseChecker,
		Now:            time.Now,
	}
}

func (c *Cycle) ledgerPath() string       { return filepath.Join(c.StateDir, "ledger.jsonl") }
func (c *Cycle) followsPath() string      { return filepath.Join(c.StateDir, "follows.json") }
func (c *Cycle) citizensPath() string     { return filepath.Join(c.StateDir, "citizens.json") }
func (c *Cycle) treePath() string         { return filepath.Join(c.StateDir, "tree.json") }
func (c *Cycle) statsPath() string        { return filepath.Join(c.StateDir, "stats.json") }
func (c *Cycle) nameCachePath() string    { return filepath.Join(c.StateDir, "name-cache.json") }
func (c *Cycle) releaseCachePath() string { return filepath.Join(c.StateDir, "release-check.json") }

// Run executes one full cycle: follows sync, ledger merge (citizens.json and
// tree.json rewritten atomically), then stats (stats.json, the name cache,
// and the daily release check).
func (c *Cycle) Run(ctx context.Context) error {
	entries, err := ReadLedger(c.ledgerPath())
	if err != nil {
		return fmt.Errorf("cycle: read ledger: %w", err)
	}
	state, err := BuildState(c.Owner, entries, c.MaxInvites, c.MaxDepth)
	if err != nil {
		return fmt.Errorf("cycle: build state: %w", err)
	}

	// 1. Follows sync.
	follows, err := c.syncFollows(ctx)
	if err != nil {
		return fmt.Errorf("cycle: sync follows: %w", err)
	}

	// 2. Merge.
	if err := writeJSONAtomic(c.citizensPath(), state.CitizensJSON(follows.Pubkeys)); err != nil {
		return fmt.Errorf("cycle: write citizens.json: %w", err)
	}
	if err := writeJSONAtomic(c.treePath(), state.Tree); err != nil {
		return fmt.Errorf("cycle: write tree.json: %w", err)
	}

	// 3. Stats.
	if err := c.generateStats(ctx, state, follows, entries); err != nil {
		return fmt.Errorf("cycle: generate stats: %w", err)
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
