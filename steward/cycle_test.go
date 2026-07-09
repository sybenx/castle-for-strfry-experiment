package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// fakeFetcher is a NostrFetcher test double: every source of network data
// is a field the test controls directly, so cycle tests never touch a real
// relay.
type fakeFetcher struct {
	kind3ByRelay map[string]*nostr.Event
	kind3Err     error

	reports    []*nostr.Event
	reportsErr error

	reactions []*nostr.Event

	// eventsByRelay simulates what each relay would resolve an event id
	// to, so tests can pin exactly which relay (local vs. a specific
	// PUBLIC_RELAYS entry) has a given note.
	eventsByRelay map[string]map[string]*nostr.Event
}

func (f *fakeFetcher) LatestKind3(ctx context.Context, relayURLs []string, pubkey string) (*nostr.Event, error) {
	if f.kind3Err != nil {
		return nil, f.kind3Err
	}
	var newest *nostr.Event
	for _, url := range relayURLs {
		ev, ok := f.kind3ByRelay[url]
		if !ok {
			continue
		}
		if newest == nil || ev.CreatedAt > newest.CreatedAt {
			newest = ev
		}
	}
	return newest, nil
}

func (f *fakeFetcher) Reports(ctx context.Context, relayURL, pubkey string) ([]*nostr.Event, error) {
	if f.reportsErr != nil {
		return nil, f.reportsErr
	}
	return f.reports, nil
}

func (f *fakeFetcher) Reactions(ctx context.Context, relayURL, pubkey string, since int64) ([]*nostr.Event, error) {
	var out []*nostr.Event
	for _, r := range f.reactions {
		if int64(r.CreatedAt) >= since {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeFetcher) Event(ctx context.Context, localRelay string, fallbackRelays []string, id string) (*nostr.Event, error) {
	for _, url := range append([]string{localRelay}, fallbackRelays...) {
		if m, ok := f.eventsByRelay[url]; ok {
			if ev, ok := m[id]; ok {
				return ev, nil
			}
		}
	}
	return nil, nil
}

// fakeCLI is a strfryCLI test double recording every delete call so tests
// can assert exactly which pubkeys were purged.
type fakeCLI struct {
	batches [][]string
	dryRuns []bool
	err     error
}

func (f *fakeCLI) DeleteByAuthors(ctx context.Context, pubkeys []string, dryRun bool) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	cp := append([]string(nil), pubkeys...)
	f.batches = append(f.batches, cp)
	f.dryRuns = append(f.dryRuns, dryRun)
	return len(pubkeys), nil
}

const testOwner = "lord"

func newTestCycle(t *testing.T, fetcher NostrFetcher, cli strfryCLI) *Cycle {
	t.Helper()
	return &Cycle{
		StateDir:     t.TempDir(),
		Owner:        testOwner,
		OwnRelay:     "ws://own",
		PublicRelays: []string{"ws://pub1", "ws://pub2"},
		MaxInvites:   5,
		MaxDepth:     4,
		Fetcher:      fetcher,
		CLI:          cli,
		Now:          func() time.Time { return time.Unix(1_000_000, 0) },
	}
}

func reportEvent(id string, tags nostr.Tags, content string) *nostr.Event {
	return &nostr.Event{ID: id, PubKey: testOwner, Kind: 1984, Tags: tags, Content: content, CreatedAt: 1}
}

func mustLedger(t *testing.T, c *Cycle) []Entry {
	t.Helper()
	entries, err := ReadLedger(c.ledgerPath())
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	return entries
}

func mustBanned(t *testing.T, c *Cycle) map[string]bool {
	t.Helper()
	entries := mustLedger(t, c)
	state, err := BuildState(c.Owner, entries, c.MaxInvites, c.MaxDepth)
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	out := make(map[string]bool)
	for _, pk := range state.BannedJSON().Pubkeys {
		out[pk] = true
	}
	return out
}

func TestCycle_ReportBansPubkeyAndPurges(t *testing.T) {
	fetcher := &fakeFetcher{
		reports: []*nostr.Event{
			reportEvent("report1", nostr.Tags{{"p", "spammer", "spam"}}, ""),
		},
	}
	cli := &fakeCLI{}
	c := newTestCycle(t, fetcher, cli)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !mustBanned(t, c)["spammer"] {
		t.Fatal("spammer should be banned after a spam report")
	}
	if len(cli.batches) != 1 || len(cli.batches[0]) != 1 || cli.batches[0][0] != "spammer" {
		t.Fatalf("expected a purge batch of [spammer], got %v", cli.batches)
	}
	if cli.dryRuns[0] {
		t.Fatal("purge-newly-banned must never be a dry run")
	}
}

func TestCycle_NudityReportDoesNotBan(t *testing.T) {
	fetcher := &fakeFetcher{
		reports: []*nostr.Event{
			reportEvent("report1", nostr.Tags{{"p", "someone", "nudity"}}, ""),
		},
	}
	c := newTestCycle(t, fetcher, &fakeCLI{})

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if mustBanned(t, c)["someone"] {
		t.Fatal("a nudity report must never ban")
	}
}

// TestCycle_ZombieBanRegression pins CLAUDE.md's ledger source-id dedupe: a
// pardoned pubkey must stay pardoned across cycles even though the exact
// same old report (event id "report1") keeps reappearing on the fixture
// relay, because reports are immortal on relays.
func TestCycle_ZombieBanRegression(t *testing.T) {
	fetcher := &fakeFetcher{
		reports: []*nostr.Event{
			reportEvent("report1", nostr.Tags{{"p", "spammer", "spam"}}, ""),
		},
	}
	cli := &fakeCLI{}
	c := newTestCycle(t, fetcher, cli)

	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbBan, Pubkey: "spammer", Source: "report1", Timestamp: 100}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbPardon, Pubkey: "spammer", Source: "api-1", Timestamp: 200}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := c.Run(context.Background()); err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
		if mustBanned(t, c)["spammer"] {
			t.Fatalf("cycle #%d: pardoned pubkey was re-banned by the same immortal report", i)
		}
	}
	if len(cli.batches) != 0 {
		t.Fatalf("no new bans should mean no purge calls, got %v", cli.batches)
	}
}

// TestCycle_NewReportAfterPardonRebans confirms the flip side: a pardon
// holds only against reports that preceded it, and a genuinely new report
// re-bans.
func TestCycle_NewReportAfterPardonRebans(t *testing.T) {
	fetcher := &fakeFetcher{
		reports: []*nostr.Event{
			reportEvent("report2", nostr.Tags{{"p", "spammer", "spam"}}, ""),
		},
	}
	cli := &fakeCLI{}
	c := newTestCycle(t, fetcher, cli)

	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbBan, Pubkey: "spammer", Source: "report1", Timestamp: 100}); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbPardon, Pubkey: "spammer", Source: "api-1", Timestamp: 200}); err != nil {
		t.Fatal(err)
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !mustBanned(t, c)["spammer"] {
		t.Fatal("a new report after a pardon must re-ban")
	}
	if len(cli.batches) != 1 || cli.batches[0][0] != "spammer" {
		t.Fatalf("expected a purge of [spammer], got %v", cli.batches)
	}
}

func TestCycle_BanDomainConvention(t *testing.T) {
	fetcher := &fakeFetcher{
		reports: []*nostr.Event{
			reportEvent("report1", nil, "this domain is spam\nban-domain: nostrmag.example\n"),
		},
	}
	cli := &fakeCLI{}
	c := newTestCycle(t, fetcher, cli)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := mustLedger(t, c)
	var found bool
	for _, e := range entries {
		if e.Verb == VerbBanDomain && e.Domain == "nostrmag.example" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a ban-domain ledger entry for nostrmag.example")
	}
	if len(cli.batches) != 0 {
		t.Fatal("domain bans are resolved to pubkeys at raid time, not cycle time — purge must not fire")
	}
}

func TestCycle_FollowsNeverShrinkOnFetchError(t *testing.T) {
	fetcher := &fakeFetcher{kind3Err: errors.New("relay unreachable")}
	c := newTestCycle(t, fetcher, &fakeCLI{})

	if err := writeJSONAtomic(c.followsPath(), FollowsSnapshot{
		Pubkeys: []string{"follow1", "follow2"}, Source: "old-kind3", CreatedAt: 500,
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := readFollows(c.followsPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Pubkeys) != 2 || after.CreatedAt != 500 {
		t.Fatalf("follows.json must survive a fetch failure unchanged, got %+v", after)
	}
}

func TestCycle_FollowsAreCitizens(t *testing.T) {
	fetcher := &fakeFetcher{}
	c := newTestCycle(t, fetcher, &fakeCLI{})

	if err := writeJSONAtomic(c.followsPath(), FollowsSnapshot{
		Pubkeys: []string{"follow1"}, Source: "kind3-a", CreatedAt: 500,
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := mustLedger(t, c)
	state, err := BuildState(c.Owner, entries, c.MaxInvites, c.MaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pk := range state.Citizens([]string{"follow1"}) {
		if pk == "follow1" {
			found = true
		}
	}
	if !found {
		t.Fatal("a synced follow must appear in the effective citizenry")
	}
}

func TestCycle_ReactWardResolvesViaLocalRelay(t *testing.T) {
	note := &nostr.Event{ID: "note1", PubKey: "reacted-author"}
	fetcher := &fakeFetcher{
		reactions: []*nostr.Event{
			{ID: "reaction1", PubKey: testOwner, Kind: 7, CreatedAt: 10, Tags: nostr.Tags{{"e", "note1"}}},
		},
		eventsByRelay: map[string]map[string]*nostr.Event{
			"ws://own": {"note1": note},
		},
	}
	cli := &fakeCLI{}
	c := newTestCycle(t, fetcher, cli)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := mustLedger(t, c)
	state, err := BuildState(c.Owner, entries, c.MaxInvites, c.MaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Elevation.IsWard("reacted-author") {
		t.Fatal("the reacted note's author should be warded")
	}
	// React-warding never purges: it's retention, not moderation.
	if len(cli.batches) != 0 {
		t.Fatal("react-warding must never trigger a purge")
	}
}

func TestCycle_ReactWardFallsBackToPublicRelays(t *testing.T) {
	note := &nostr.Event{ID: "note1", PubKey: "reacted-author"}
	fetcher := &fakeFetcher{
		reactions: []*nostr.Event{
			{ID: "reaction1", PubKey: testOwner, Kind: 7, CreatedAt: 10, Tags: nostr.Tags{{"e", "note1"}}},
		},
		// Absent from the local relay; only the second PUBLIC_RELAYS entry
		// has it, exercising the full fallback chain.
		eventsByRelay: map[string]map[string]*nostr.Event{
			"ws://pub2": {"note1": note},
		},
	}
	c := newTestCycle(t, fetcher, &fakeCLI{})

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := mustLedger(t, c)
	state, err := BuildState(c.Owner, entries, c.MaxInvites, c.MaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Elevation.IsWard("reacted-author") {
		t.Fatal("react-warding must fall back to PUBLIC_RELAYS when the note is absent locally")
	}
}

func TestCycle_ReactWardSkipsOwnerAndAlreadyElevated(t *testing.T) {
	ownerNote := &nostr.Event{ID: "note-owner", PubKey: testOwner}
	favoriteNote := &nostr.Event{ID: "note-fav", PubKey: "favorite-pk"}
	fetcher := &fakeFetcher{
		reactions: []*nostr.Event{
			{ID: "reaction-owner", PubKey: testOwner, Kind: 7, CreatedAt: 10, Tags: nostr.Tags{{"e", "note-owner"}}},
			{ID: "reaction-fav", PubKey: testOwner, Kind: 7, CreatedAt: 11, Tags: nostr.Tags{{"e", "note-fav"}}},
		},
		eventsByRelay: map[string]map[string]*nostr.Event{
			"ws://own": {"note-owner": ownerNote, "note-fav": favoriteNote},
		},
	}
	c := newTestCycle(t, fetcher, &fakeCLI{})

	if err := AppendLedger(c.ledgerPath(), Entry{Verb: VerbElevate, Pubkey: "favorite-pk", Public: true, Source: "api-1", Timestamp: 1}); err != nil {
		t.Fatal(err)
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := mustLedger(t, c)
	state, err := BuildState(c.Owner, entries, c.MaxInvites, c.MaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	if state.Elevation.IsElevated(testOwner) {
		t.Fatal("react-warding must never elevate the Lord")
	}
	if !state.Elevation.IsFavorite("favorite-pk") {
		t.Fatal("liking a favorite's note must never demote their public star into a ward")
	}
	count := 0
	for _, e := range entries {
		if e.Pubkey == "favorite-pk" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly the original elevate entry for favorite-pk, got %d entries", count)
	}
}

func TestCycle_ReactWardWatermarkPreventsReprocessing(t *testing.T) {
	note := &nostr.Event{ID: "note1", PubKey: "reacted-author"}
	fetcher := &fakeFetcher{
		reactions: []*nostr.Event{
			{ID: "reaction1", PubKey: testOwner, Kind: 7, CreatedAt: 10, Tags: nostr.Tags{{"e", "note1"}}},
		},
		eventsByRelay: map[string]map[string]*nostr.Event{
			"ws://own": {"note1": note},
		},
	}
	c := newTestCycle(t, fetcher, &fakeCLI{})

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run #2: %v", err)
	}

	entries := mustLedger(t, c)
	count := 0
	for _, e := range entries {
		if e.Pubkey == "reacted-author" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the reacted author must be warded exactly once, got %d elevate entries", count)
	}
}

func TestChunkStrings(t *testing.T) {
	items := make([]string, 120)
	for i := range items {
		items[i] = "pk"
	}
	batches := chunkStrings(items, 50)
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches of <=50, got %d", len(batches))
	}
	if len(batches[0]) != 50 || len(batches[1]) != 50 || len(batches[2]) != 20 {
		t.Fatalf("unexpected batch sizes: %d %d %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
	if chunkStrings(nil, 50) != nil {
		t.Fatal("chunking an empty slice should yield nil")
	}
}
