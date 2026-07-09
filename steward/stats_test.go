package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func mustRun(t *testing.T, c *Cycle) {
	t.Helper()
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func readRaw(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestStats_WardsNeverAppear greps the actual served payloads (stats.json
// and name-cache.json), not just the Go structs, per CLAUDE.md's testing
// checklist: "wards absent... grep the served payloads in a test, not by
// eye."
func TestStats_WardsNeverAppear(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{
		kind0ByRelay: map[string]map[string]*nostr.Event{
			"ws://own": {
				"member1": {Content: `{"name":"Member One"}`},
				"ward1":   {Content: `{"name":"Ward One"}`},
			},
		},
	})
	c.Scanner = &fakeScanner{events: []fakeStoredEvent{
		{Pubkey: testOwner, CreatedAt: 100},
		{Pubkey: "member1", CreatedAt: 200},
		{Pubkey: "ward1", CreatedAt: 300},
	}}

	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "member1", InvitedBy: testOwner, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbElevate, Pubkey: "member1", Public: true, Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbElevate, Pubkey: "ward1", Public: false, Timestamp: 3}); err != nil {
		t.Fatal(err)
	}

	mustRun(t, c)

	statsRaw := readRaw(t, c.statsPath())
	if strings.Contains(statsRaw, "ward1") || strings.Contains(statsRaw, "Ward One") {
		t.Fatalf("stats.json leaked ward data: %s", statsRaw)
	}
	var stats Stats
	if err := json.Unmarshal([]byte(statsRaw), &stats); err != nil {
		t.Fatalf("stats.json did not parse: %v", err)
	}
	if stats.Citizens.Favored != 1 {
		t.Fatalf("citizens.favored = %d, want 1 (member1 only, ward1 excluded)", stats.Citizens.Favored)
	}

	cacheRaw := readRaw(t, c.nameCachePath())
	if strings.Contains(cacheRaw, "ward1") || strings.Contains(cacheRaw, "Ward One") {
		t.Fatalf("name-cache.json leaked ward data: %s", cacheRaw)
	}
	if !strings.Contains(cacheRaw, "Member One") {
		t.Fatalf("name-cache.json missing the public favorite's name: %s", cacheRaw)
	}
}

func TestStats_CitizenAndOuterLandsCounts(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{})
	c.Scanner = &fakeScanner{events: []fakeStoredEvent{
		{Pubkey: testOwner, CreatedAt: 100},
		{Pubkey: testOwner, CreatedAt: 101},
		{Pubkey: "m1", CreatedAt: 200},
		{Pubkey: "m1", CreatedAt: 201},
		{Pubkey: "m1", CreatedAt: 202},
		{Pubkey: "f1", CreatedAt: 300},
		{Pubkey: "w1", CreatedAt: 400}, // ward: a citizen, but not publicly counted
		{Pubkey: "w1", CreatedAt: 401},
		{Pubkey: "w1", CreatedAt: 402},
		{Pubkey: "w1", CreatedAt: 403},
		{Pubkey: "w1", CreatedAt: 404},
		{Pubkey: "stranger1", CreatedAt: 50}, // oldest of all
		{Pubkey: "stranger2", CreatedAt: 900},
	}}
	if err := writeJSONAtomic(c.followsPath(), FollowsSnapshot{Pubkeys: []string{"f1"}, Source: "src", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "m1", InvitedBy: testOwner, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbElevate, Pubkey: "w1", Public: false, Timestamp: 2}); err != nil {
		t.Fatal(err)
	}

	mustRun(t, c)

	var stats Stats
	if err := json.Unmarshal([]byte(readRaw(t, c.statsPath())), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.TheLord.Events != 2 {
		t.Fatalf("the_lord.events = %d, want 2", stats.TheLord.Events)
	}
	if stats.Citizens.Tree != 1 || stats.Citizens.Follows != 1 || stats.Citizens.Favored != 0 {
		t.Fatalf("citizens = %+v, want tree=1 follows=1 favored=0", stats.Citizens)
	}
	if stats.Citizens.Events != 4 { // m1(3) + f1(1); w1 is a ward, excluded from the public count
		t.Fatalf("citizens.events = %d, want 4 (public components only)", stats.Citizens.Events)
	}
	if stats.OuterLands.Events != 2 { // stranger1 + stranger2 only; w1 IS a citizen (a ward) so its events are not outer lands
		t.Fatalf("outer_lands.events = %d, want 2", stats.OuterLands.Events)
	}
	if stats.OuterLands.Oldest != 50 {
		t.Fatalf("outer_lands.oldest = %d, want 50", stats.OuterLands.Oldest)
	}
}

func TestStats_EvictedInGrace(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := newTestCycle(t, &fakeFetcher{})
	c.Now = func() time.Time { return now }
	c.OuterTTLDays = 30

	// m1: invited, then removed recently -> inside grace.
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "m1", InvitedBy: testOwner, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbRemove, Pubkey: "m1", Timestamp: now.Unix() - 86400}); err != nil {
		t.Fatal(err)
	}

	// m2: invited, removed long ago -> past grace, must not appear.
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "m2", InvitedBy: testOwner, Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbRemove, Pubkey: "m2", Timestamp: now.Unix() - int64(60*86400)}); err != nil {
		t.Fatal(err)
	}

	// m3: invited, removed recently, but re-invited -> regained citizenship,
	// must not appear in the evicted list even though state.Evicted still
	// carries the old timestamp.
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "m3", InvitedBy: testOwner, Timestamp: 3}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbRemove, Pubkey: "m3", Timestamp: now.Unix() - 3600}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "m3", InvitedBy: testOwner, Timestamp: now.Unix() - 1800}); err != nil {
		t.Fatal(err)
	}

	mustRun(t, c)

	var stats Stats
	if err := json.Unmarshal([]byte(readRaw(t, c.statsPath())), &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.Evicted) != 1 || stats.Evicted[0].Pubkey != "m1" {
		t.Fatalf("evicted = %+v, want exactly m1", stats.Evicted)
	}
	wantExpires := (now.Unix() - 86400) + 30*86400
	if stats.Evicted[0].Expires != wantExpires {
		t.Fatalf("m1 expires = %d, want %d", stats.Evicted[0].Expires, wantExpires)
	}
}

func TestNameCache_StalenessAndPruning(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fetcher := &fakeFetcher{kind0ByRelay: map[string]map[string]*nostr.Event{
		"ws://own": {"m1": {Content: `{"name":"First Fetch"}`}},
	}}
	c := newTestCycle(t, fetcher)
	c.Now = func() time.Time { return now }

	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "m1", InvitedBy: testOwner, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, c)

	cache, err := readNameCache(c.nameCachePath())
	if err != nil {
		t.Fatal(err)
	}
	if cache["m1"].Name != "First Fetch" {
		t.Fatalf("name cache = %+v, want m1 = First Fetch", cache)
	}

	// Advance time within the staleness window and change what the relay
	// would return: the cached entry must be reused, not refetched.
	now = now.Add(1 * time.Hour)
	fetcher.kind0ByRelay["ws://own"]["m1"] = &nostr.Event{Content: `{"name":"Should Not Appear"}`}
	mustRun(t, c)
	cache, err = readNameCache(c.nameCachePath())
	if err != nil {
		t.Fatal(err)
	}
	if cache["m1"].Name != "First Fetch" {
		t.Fatalf("name cache should not refresh inside the staleness window, got %+v", cache)
	}

	// m1 is removed from the tree; once its OUTER_TTL_DAYS grace window
	// also passes it is no longer a subject at all, and the cache entry
	// must be dropped, not kept around stale.
	c.OuterTTLDays = 1
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbRemove, Pubkey: "m1", Timestamp: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(nameCacheStaleness + 2*24*time.Hour)
	mustRun(t, c)
	cache, err = readNameCache(c.nameCachePath())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache["m1"]; ok {
		t.Fatalf("name cache must drop m1 once it's no longer tree/favorite/evicted-in-grace, got %+v", cache)
	}
}

func TestCheckRelease_CachesDaily(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := newTestCycle(t, &fakeFetcher{})
	c.Now = func() time.Time { return now }
	checker := &fakeReleaseChecker{latest: "v0.3.0"}
	c.ReleaseChecker = checker

	latest, err := c.checkRelease(context.Background())
	if err != nil || latest != "v0.3.0" {
		t.Fatalf("checkRelease = %q, %v; want v0.3.0, nil", latest, err)
	}

	// A later call within the interval must reuse the cache, not the
	// checker, even if the checker starts failing.
	checker.latest = "v0.4.0"
	checker.err = context.Canceled
	latest, err = c.checkRelease(context.Background())
	if err != nil || latest != "v0.3.0" {
		t.Fatalf("checkRelease within interval = %q, %v; want cached v0.3.0, nil", latest, err)
	}

	// Past the interval, a failing checker keeps the last cached value.
	now = now.Add(releaseCheckInterval + time.Hour)
	latest, err = c.checkRelease(context.Background())
	if err != nil || latest != "v0.3.0" {
		t.Fatalf("checkRelease after a failed refresh = %q, %v; want stale cached v0.3.0, nil", latest, err)
	}

	// A successful refresh past the interval updates the cache.
	checker.err = nil
	latest, err = c.checkRelease(context.Background())
	if err != nil || latest != "v0.4.0" {
		t.Fatalf("checkRelease after a successful refresh = %q, %v; want v0.4.0, nil", latest, err)
	}
}
