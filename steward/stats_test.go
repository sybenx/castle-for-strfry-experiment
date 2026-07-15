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
	if stats.OuterLands.TTLDays != 30 { // the standing OUTER_TTL_DAYS, so towncrier's raid control can pre-fill it
		t.Fatalf("outer_lands.ttl_days = %d, want 30", stats.OuterLands.TTLDays)
	}
}

// TestStats_LastRaidExcludesDryRuns: stats.json's last_at/last_purged must
// reflect a REAL purge, not a preview — otherwise a Lord who only ever runs
// previews (or is stuck on the RAID_DRY_RUN=true default) would see a
// "last raid" that never actually happened.
func TestStats_LastRaidExcludesDryRuns(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{})
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbRaidRun, Purged: 99, DryRun: true, Timestamp: 500}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbRaidRun, Purged: 7, DryRun: false, Timestamp: 600}); err != nil {
		t.Fatal(err)
	}

	mustRun(t, c)

	var stats Stats
	if err := json.Unmarshal([]byte(readRaw(t, c.statsPath())), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Raids.LastAt != 600 || stats.Raids.LastPurged != 7 {
		t.Fatalf("raids = %+v, want the real raid (ts=600, purged=7), not the dry-run preview", stats.Raids)
	}
}

func TestStats_RaidNextNullWhenCronEmpty(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{})
	c.RaidCron = ""

	mustRun(t, c)

	var stats Stats
	if err := json.Unmarshal([]byte(readRaw(t, c.statsPath())), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Raids.Next != nil {
		t.Fatalf("raids.next = %v, want null when RAID_CRON is empty", *stats.Raids.Next)
	}
}

func TestStats_RaidNextFromCron(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{})
	c.RaidCron = "0 0 * * *"
	c.Now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }

	mustRun(t, c)

	var stats Stats
	if err := json.Unmarshal([]byte(readRaw(t, c.statsPath())), &stats); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Unix()
	if stats.Raids.Next == nil || *stats.Raids.Next != want {
		t.Fatalf("raids.next = %v, want %d", stats.Raids.Next, want)
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

// --- the census ---

func readCensus(t *testing.T, c *Cycle) Census {
	t.Helper()
	var census Census
	if err := json.Unmarshal([]byte(readRaw(t, c.censusPath())), &census); err != nil {
		t.Fatalf("census.json did not parse: %v", err)
	}
	return census
}

func TestCensus_Aggregation(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{})
	c.Scanner = &fakeScanner{events: []fakeStoredEvent{
		{Pubkey: testOwner, Kind: 1, CreatedAt: 100},
		{Pubkey: testOwner, Kind: 0, CreatedAt: 150},
		{Pubkey: "m1", Kind: 1, CreatedAt: 200},
		{Pubkey: "m1", Kind: 1, CreatedAt: 300},
		{Pubkey: "m1", Kind: 7, CreatedAt: 250},
		{Pubkey: "stranger1", Kind: 1, CreatedAt: 400},
		{Pubkey: "stranger2", Kind: 1, CreatedAt: 50},
		{Pubkey: "stranger2", Kind: 1, CreatedAt: 60},
		// ephemeral kinds are strfry's business (NIP-16); ignored in stats
		{Pubkey: "stranger1", Kind: 20001, CreatedAt: 500},
	}}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbInvite, Pubkey: "m1", InvitedBy: testOwner, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}

	mustRun(t, c)
	census := readCensus(t, c)

	if census.Events != 8 { // 9 stored minus the ephemeral one
		t.Fatalf("events = %d, want 8 (ephemeral kind excluded)", census.Events)
	}
	if census.Authors != 4 {
		t.Fatalf("authors = %d, want 4", census.Authors)
	}
	// kinds: 1 -> 6, 0 -> 1, 7 -> 1; kind 20001 absent
	if len(census.Kinds) != 3 || census.Kinds[0].Kind != 1 || census.Kinds[0].Events != 6 {
		t.Fatalf("kinds = %+v, want kind 1 first with 6 events and no ephemeral kind", census.Kinds)
	}
	for _, k := range census.Kinds {
		if isEphemeralKind(k.Kind) {
			t.Fatalf("ephemeral kind %d leaked into the census", k.Kind)
		}
	}
	// top authors: m1(3) first, then owner(2)/stranger2(2) by pubkey, stranger1(1)
	if census.TopAuthors[0].Pubkey != "m1" || census.TopAuthors[0].Events != 3 {
		t.Fatalf("top_authors[0] = %+v, want m1 with 3", census.TopAuthors[0])
	}
	if census.TopAuthors[0].FirstSeen != 200 || census.TopAuthors[0].LastSeen != 300 {
		t.Fatalf("m1 first/last = %d/%d, want 200/300", census.TopAuthors[0].FirstSeen, census.TopAuthors[0].LastSeen)
	}
	if len(census.AllAuthors) != 4 {
		t.Fatalf("all_authors has %d entries, want 4", len(census.AllAuthors))
	}
	// stale outer: strangers only (owner and m1 are public citizens),
	// least-recently-active first: stranger2 (lastSeen 60) then stranger1 (400)
	if len(census.StaleOuter) != 2 || census.StaleOuter[0].Pubkey != "stranger2" || census.StaleOuter[1].Pubkey != "stranger1" {
		t.Fatalf("stale_outer = %+v, want [stranger2, stranger1]", census.StaleOuter)
	}
}

// TestCensus_WardIndistinguishable is the census's ward-privacy property:
// a ward's pubkey MAY appear (their events are anonymously queryable from
// the relay like anyone's), but nothing may classify them differently from
// a stranger — the encrypted split and stale_outer must both treat a ward
// as outer, because they are computed from PUBLIC citizen components only.
func TestCensus_WardIndistinguishable(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{})
	c.Scanner = &fakeScanner{events: []fakeStoredEvent{
		{Pubkey: testOwner, Kind: 4, CreatedAt: 100},      // encrypted, public citizen
		{Pubkey: "ward1", Kind: 4, CreatedAt: 200},        // encrypted, ward
		{Pubkey: "ward1", Kind: 1, CreatedAt: 210},        //
		{Pubkey: "stranger1", Kind: 1059, CreatedAt: 300}, // encrypted, stranger
	}}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbElevate, Pubkey: "ward1", Public: false, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}

	mustRun(t, c)
	census := readCensus(t, c)

	// The ward's encrypted event counts on the OUTER side: classifying it
	// as citizen would reveal ward status by subtraction.
	if census.Encrypted.PublicCitizens != 1 || census.Encrypted.Outer != 2 {
		t.Fatalf("encrypted = %+v, want public_citizens=1 (the Lord) outer=2 (ward + stranger)", census.Encrypted)
	}
	// The ward appears in stale_outer exactly like a stranger.
	foundWard := false
	for _, e := range census.StaleOuter {
		if e.Pubkey == "ward1" {
			foundWard = true
		}
	}
	if !foundWard {
		t.Fatalf("stale_outer = %+v, want ward1 present as an ordinary outer author", census.StaleOuter)
	}
	// And no author entry carries any field beyond the event-derived four
	// — a status marker (ward/citizen/favorite/protected) on an author row
	// is exactly the leak the census rule forbids. Checked against the
	// served bytes, not the Go struct, per the standing grep-the-payload
	// discipline.
	var payload struct {
		AllAuthors []map[string]any `json:"all_authors"`
	}
	if err := json.Unmarshal([]byte(readRaw(t, c.censusPath())), &payload); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"pubkey": true, "events": true, "first_seen": true, "last_seen": true}
	for _, entry := range payload.AllAuthors {
		for key := range entry {
			if !allowed[key] {
				t.Fatalf("author entry %v carries non-event-derived field %q", entry, key)
			}
		}
	}
}

// TestVersionStatus pins the one place version ordering is decided (CLAUDE.md:
// "Version comparison is steward's job, not towncrier's"). The cases are the
// real strings the Makefile's `git describe --tags --always --dirty` and
// GitHub's tag_name can produce — including every misclassification the
// Phase 8 code review found in the old client-side regex approach.
func TestVersionStatus(t *testing.T) {
	cases := []struct {
		running, latest, want string
	}{
		{"v0.6.0", "v0.6.0", "current"},           // exact release build, up to date
		{"v0.4.1-5-gabc1234", "v0.6.0", "update"}, // describe build of an OLD base: genuinely behind
		{"v0.6.0-5-gabc1234", "v0.6.0", "ahead"},  // describe build past the latest tag
		{"v0.6.0-dirty", "v0.6.0", "ahead"},       // dirty build of the latest tag
		{"v0.7.0", "v0.6.0", "ahead"},             // tagged ahead of the published release
		{"dev", "v0.6.0", "unknown"},              // ldflags fallback: position unknowable
		{"57920d8", "v0.6.0", "unknown"},          // git describe --always bare hash (shallow clone)
		{"0.6.0", "v0.6.0", "current"},            // unprefixed running, same release
		{"v0.6.0", "0.6.1", "update"},             // unprefixed latest still compares
		{"v0.6.0", "nightly", "unknown"},          // unparseable latest: never an update prompt
		{"v0.9.0", "v0.10.0", "update"},           // numeric, not lexicographic
		{"v0.10.0", "v0.9.0", "ahead"},            //
		{"v1.0.0", "v1.0.0-rc1", "current"},       // equal cores, exact running: no downgrade prompt
		{"dev", "", ""},                           // no latest known: no banner at all
		{"v0.6.0", "", ""},                        //
		{"dev", "dev", "current"},                 // identical unparseable strings
	}
	for _, tc := range cases {
		if got := versionStatus(tc.running, tc.latest); got != tc.want {
			t.Errorf("versionStatus(%q, %q) = %q, want %q", tc.running, tc.latest, got, tc.want)
		}
	}
}

func TestStats_VersionStatusInStatsJSON(t *testing.T) {
	c := newTestCycle(t, &fakeFetcher{})
	c.RunningVersion = "v0.4.1-5-gabc1234"
	c.ReleaseChecker = &fakeReleaseChecker{latest: "v0.6.0"}

	mustRun(t, c)

	var stats Stats
	if err := json.Unmarshal([]byte(readRaw(t, c.statsPath())), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Version.Status != "update" {
		t.Fatalf("version.status = %q, want %q (stale describe build must get the update nudge)", stats.Version.Status, "update")
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
