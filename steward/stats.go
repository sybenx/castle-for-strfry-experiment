// stats.json generation: batched strfry scan counts, the kind-0 name
// cache, and the daily GitHub release check. Lands in Phase 3b.
// See CLAUDE.md, stats.json schema, and "Name cache and update banner".
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"time"
)

// scanBatchSize mirrors raid.go's deleteBatchSize: keeps each `strfry scan`
// command line bounded regardless of how many authors it targets.
const scanBatchSize = 50

// strfryScanner is steward's read path into strfry's stored events, reached
// via `docker exec` (see raid.go's strfryCLI for the write-side wrapper).
// Upstream strfry's `scan` has neither a `--count` flag nor an "author NOT
// IN" filter, so Count gets its number by streaming scan's NDJSON output
// and counting lines (never parsing or slurping them), and outer-lands
// totals — which need "belongs to no citizen" — stream the whole table
// once and classify client-side, the same pattern CLAUDE.md's raid
// pseudocode uses. Interfaced so stats tests can fake it without a live
// strfry.
type strfryScanner interface {
	// Count returns how many stored events match filter (a raw NIP-01
	// filter, e.g. {"authors": [...]}).
	Count(ctx context.Context, filter map[string]any) (int, error)

	// ScanAll streams every stored event's author and timestamp to fn, one
	// at a time ("stream, don't slurp" — CLAUDE.md's raid pseudocode).
	ScanAll(ctx context.Context, fn func(pubkey string, createdAt int64)) error
}

// countByAuthors sums Count across pubkeys in scanBatchSize-sized batches so
// a large author list can't build one oversized command line. An empty
// pubkeys list costs nothing and never touches strfry (an empty "authors"
// filter is not a safe stand-in for "no filter").
func countByAuthors(ctx context.Context, scanner strfryScanner, pubkeys []string) (int, error) {
	total := 0
	for _, batch := range chunkStrings(pubkeys, scanBatchSize) {
		n, err := scanner.Count(ctx, map[string]any{"authors": batch})
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// dockerStrfryScanner is the real strfryScanner, shelling out to
// `docker exec <container> strfry scan <filter>`.
type dockerStrfryScanner struct {
	Container string
}

func (d *dockerStrfryScanner) Count(ctx context.Context, filter map[string]any) (int, error) {
	data, err := json.Marshal(filter)
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", d.Container, strfryBinPath, "scan", string(data))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	count := 0
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) > 0 {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if err := cmd.Wait(); err != nil {
		return 0, fmt.Errorf("strfry scan: %w", err)
	}
	return count, nil
}

func (d *dockerStrfryScanner) ScanAll(ctx context.Context, fn func(pubkey string, createdAt int64)) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", d.Container, strfryBinPath, "scan", "{}")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev struct {
			Pubkey    string `json:"pubkey"`
			CreatedAt int64  `json:"created_at"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // a malformed line shouldn't sink the whole scan
		}
		fn(ev.Pubkey, ev.CreatedAt)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("strfry scan: %w", err)
	}
	return nil
}

// --- stats.json ---

// Stats is stats.json's schema (CLAUDE.md). Ward counts appear nowhere in
// it: Citizens is computed from public components only (tree ∪ follows ∪
// public favorites), never wards.
type Stats struct {
	UpdatedAt  int64           `json:"updated_at"`
	Version    VersionInfo     `json:"version"`
	TheLord    LordStats       `json:"the_lord"`
	Citizens   CitizenStats    `json:"citizens"`
	Evicted    []EvictedEntry  `json:"evicted"`
	OuterLands OuterLandsStats `json:"outer_lands"`
	Raids      RaidStats       `json:"raids"`
	Invites    InviteStats     `json:"invites"`
}

type VersionInfo struct {
	Running string `json:"running"`
	Latest  string `json:"latest,omitempty"`
}

type LordStats struct {
	Pubkey string `json:"pubkey"`
	Events int    `json:"events"`
}

type CitizenStats struct {
	Tree    int `json:"tree"`
	Follows int `json:"follows"`
	Favored int `json:"favored"`
	Events  int `json:"events"`
}

type EvictedEntry struct {
	Pubkey  string `json:"pubkey"`
	Expires int64  `json:"expires"`
}

type OuterLandsStats struct {
	Events int   `json:"events"`
	Oldest int64 `json:"oldest"`
}

// RaidStats.Next stays nil until Phase 4 wires RAID_CRON scheduling; it is
// always null while RAID_CRON is empty regardless (CLAUDE.md's schema).
type RaidStats struct {
	Next       *int64 `json:"next"`
	LastAt     int64  `json:"last_at,omitempty"`
	LastPurged int    `json:"last_purged,omitempty"`
}

type InviteStats struct {
	MaxPerMember int `json:"max_per_member"`
	MaxDepth     int `json:"max_depth"`
}

// publicCitizenPubkeys is tree ∪ follows ∪ public favorites, excluding the
// Lord himself (who has his own top-level stats entry). This is the ONLY
// set stats.json's Citizens counts are computed from — CLAUDE.md's privacy
// invariant: "Public citizen counts are computed from public components
// only... if wards were included, subtraction would reveal their number."
func publicCitizenPubkeys(state *State, follows []string) []string {
	set := make(map[string]bool)
	for pk := range state.Tree.Members {
		set[pk] = true
	}
	for _, pk := range follows {
		set[pk] = true
	}
	for pk, r := range state.Elevation.Records {
		if r.Public {
			set[pk] = true
		}
	}
	delete(set, state.Owner)
	out := make([]string, 0, len(set))
	for pk := range set {
		out = append(out, pk)
	}
	sort.Strings(out)
	return out
}

// evictedInGrace returns evicted members still inside OUTER_TTL_DAYS of
// their removal, sorted by soonest-expiring first. A pubkey that has since
// regained citizenship (re-invited, re-followed, elevated) is excluded —
// state.Evicted is a replay artifact that never prunes itself, since
// citizenship flows from the CURRENT tree/follows/elevation, not history.
func evictedInGrace(state *State, follows []string, now int64, outerTTLDays int) []EvictedEntry {
	citizens := make(map[string]bool)
	for _, pk := range state.Citizens(follows) {
		citizens[pk] = true
	}
	graceSeconds := int64(outerTTLDays) * 86400
	out := []EvictedEntry{}
	for pk, ts := range state.Evicted {
		if citizens[pk] {
			continue
		}
		expires := ts + graceSeconds
		if expires > now {
			out = append(out, EvictedEntry{Pubkey: pk, Expires: expires})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Expires != out[j].Expires {
			return out[i].Expires < out[j].Expires
		}
		return out[i].Pubkey < out[j].Pubkey
	})
	return out
}

// lastRaidRun returns the most recent raid-run ledger entry, if any.
func lastRaidRun(entries []Entry) (at int64, purged int, ok bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Verb == VerbRaidRun {
			return entries[i].Timestamp, entries[i].Purged, true
		}
	}
	return 0, 0, false
}

// generateStats computes stats.json from the current replayed State and
// writes it atomically. entries is the full ledger (for the last-raid
// lookup); follows is the current follows snapshot.
func (c *Cycle) generateStats(ctx context.Context, state *State, follows FollowsSnapshot, entries []Entry) error {
	now := c.Now().Unix()

	lordEvents, err := countByAuthors(ctx, c.Scanner, []string{state.Owner})
	if err != nil {
		return fmt.Errorf("stats: count the Lord's events: %w", err)
	}

	publicCitizens := publicCitizenPubkeys(state, follows.Pubkeys)
	citizenEvents, err := countByAuthors(ctx, c.Scanner, publicCitizens)
	if err != nil {
		return fmt.Errorf("stats: count citizens' events: %w", err)
	}

	allCitizens := make(map[string]bool)
	for _, pk := range state.Citizens(follows.Pubkeys) {
		allCitizens[pk] = true
	}
	outerEvents := 0
	var outerOldest int64
	err = c.Scanner.ScanAll(ctx, func(pubkey string, createdAt int64) {
		if allCitizens[pubkey] {
			return
		}
		outerEvents++
		if outerOldest == 0 || createdAt < outerOldest {
			outerOldest = createdAt
		}
	})
	if err != nil {
		return fmt.Errorf("stats: scan the Outer Lands: %w", err)
	}

	evicted := evictedInGrace(state, follows.Pubkeys, now, c.OuterTTLDays)

	favoredCount := 0
	for _, r := range state.Elevation.Records {
		if r.Public {
			favoredCount++
		}
	}

	version := VersionInfo{Running: c.RunningVersion}
	if latest, err := c.checkRelease(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "steward: release check: %v\n", err)
	} else {
		version.Latest = latest
	}

	raids := RaidStats{Next: nil}
	if at, purged, ok := lastRaidRun(entries); ok {
		raids.LastAt = at
		raids.LastPurged = purged
	}

	stats := Stats{
		UpdatedAt: now,
		Version:   version,
		TheLord:   LordStats{Pubkey: state.Owner, Events: lordEvents},
		Citizens: CitizenStats{
			Tree:    len(state.Tree.Members),
			Follows: len(follows.Pubkeys),
			Favored: favoredCount,
			Events:  citizenEvents,
		},
		Evicted:    evicted,
		OuterLands: OuterLandsStats{Events: outerEvents, Oldest: outerOldest},
		Raids:      raids,
		Invites:    InviteStats{MaxPerMember: c.MaxInvites, MaxDepth: c.MaxDepth},
	}

	if err := writeJSONAtomic(c.statsPath(), stats); err != nil {
		return fmt.Errorf("stats: write stats.json: %w", err)
	}

	subjects := nameCacheSubjects(state, evicted)
	if err := c.refreshNameCache(ctx, subjects); err != nil {
		return fmt.Errorf("stats: refresh name cache: %w", err)
	}
	return nil
}

// --- kind-0 name cache ---

// NameCacheEntry is one cached kind-0 profile plus when it was fetched, so
// refreshNameCache can tell a fresh entry from a stale one.
type NameCacheEntry struct {
	Name      string `json:"name,omitempty"`
	Picture   string `json:"picture,omitempty"`
	FetchedAt int64  `json:"fetched_at"`
}

// NameCache is name-cache.json's schema: pubkey -> cached profile.
type NameCache map[string]NameCacheEntry

// nameCacheStaleness is the lazy-refresh threshold: an entry younger than
// this is reused as-is rather than re-fetched.
const nameCacheStaleness = 24 * time.Hour

// nameCacheSubjects is tree members ∪ public favorites ∪ evicted-in-grace —
// CLAUDE.md's exact name-cache coverage. Never wards.
func nameCacheSubjects(state *State, evicted []EvictedEntry) []string {
	set := make(map[string]bool)
	for pk := range state.Tree.Members {
		set[pk] = true
	}
	for pk, r := range state.Elevation.Records {
		if r.Public {
			set[pk] = true
		}
	}
	for _, e := range evicted {
		set[e.Pubkey] = true
	}
	out := make([]string, 0, len(set))
	for pk := range set {
		out = append(out, pk)
	}
	sort.Strings(out)
	return out
}

func readNameCache(path string) (NameCache, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NameCache{}, nil
	}
	if err != nil {
		return NameCache{}, err
	}
	cache := NameCache{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return NameCache{}, err
	}
	return cache, nil
}

// refreshNameCache rewrites name-cache.json to contain exactly the current
// subjects — anyone no longer a subject (lowered, evicted past grace,
// removed from the tree) is dropped, not kept around stale. Entries still
// fresh are reused; missing or stale ones are re-fetched via
// LatestKind0s (own relay first, PUBLIC_RELAYS fallback); a failed refetch
// keeps the old entry rather than losing the name outright.
func (c *Cycle) refreshNameCache(ctx context.Context, subjects []string) error {
	path := c.nameCachePath()
	old, err := readNameCache(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: read name cache: %v (starting fresh)\n", err)
		old = NameCache{}
	}

	now := c.Now().Unix()
	next := make(NameCache, len(subjects))
	var stale []string
	for _, pk := range subjects {
		if e, ok := old[pk]; ok && now-e.FetchedAt < int64(nameCacheStaleness.Seconds()) {
			next[pk] = e
		} else {
			stale = append(stale, pk)
		}
	}

	if len(stale) > 0 {
		relays := append([]string{c.OwnRelay}, c.PublicRelays...)
		events, err := c.Fetcher.LatestKind0s(ctx, relays, stale)
		if err != nil {
			fmt.Fprintf(os.Stderr, "steward: kind-0 refresh failed: %v\n", err)
			events = nil
		}
		for _, pk := range stale {
			if ev, ok := events[pk]; ok {
				var profile struct {
					Name    string `json:"name"`
					Picture string `json:"picture"`
				}
				if json.Unmarshal([]byte(ev.Content), &profile) == nil {
					next[pk] = NameCacheEntry{Name: profile.Name, Picture: profile.Picture, FetchedAt: now}
					continue
				}
			}
			if e, ok := old[pk]; ok {
				next[pk] = e // keep the stale entry rather than losing the name
			}
		}
	}

	return writeJSONAtomic(path, next)
}

// --- daily GitHub release check ---

// ReleaseChecker answers "what's the latest published release tag", so
// stats.json can show the Lord an update banner. Interfaced so tests never
// hit the network.
type ReleaseChecker interface {
	LatestRelease(ctx context.Context) (tag string, err error)
}

// releaseCheckInterval: "once a day" per CLAUDE.md's update-check cadence.
const releaseCheckInterval = 24 * time.Hour

const githubReleaseURL = "https://api.github.com/repos/sybenx/castle-for-strfry-experiment/releases/latest"

type githubReleaseChecker struct{}

func (githubReleaseChecker) LatestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases: unexpected status %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

// releaseCacheEntry is release-check.json's schema: the daily cache that
// keeps the GitHub check to once a day rather than once a cycle.
type releaseCacheEntry struct {
	CheckedAt int64  `json:"checked_at"`
	Latest    string `json:"latest"`
}

// checkRelease returns the cached latest-release tag, refreshing it via
// c.ReleaseChecker only once releaseCheckInterval has elapsed. A failed
// refresh keeps whatever was last cached, logged but never fatal.
func (c *Cycle) checkRelease(ctx context.Context) (string, error) {
	if c.ReleaseChecker == nil {
		return "", nil
	}
	path := c.releaseCachePath()
	var cache releaseCacheEntry
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cache)
	}

	now := c.Now().Unix()
	if now-cache.CheckedAt < int64(releaseCheckInterval.Seconds()) {
		return cache.Latest, nil
	}

	latest, err := c.ReleaseChecker.LatestRelease(ctx)
	if err != nil {
		return cache.Latest, nil
	}
	next := releaseCacheEntry{CheckedAt: now, Latest: latest}
	if err := writeJSONAtomic(path, next); err != nil {
		return latest, fmt.Errorf("write release cache: %w", err)
	}
	return latest, nil
}
