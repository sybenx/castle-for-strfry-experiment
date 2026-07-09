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
	kind0ByRelay map[string]map[string]*nostr.Event
	kind0Err     error
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

// kind0ByRelay is relay URL -> pubkey -> event, so tests can exercise the
// "own relay first, PUBLIC_RELAYS fallback" order explicitly.
func (f *fakeFetcher) LatestKind0s(ctx context.Context, relayURLs []string, pubkeys []string) (map[string]*nostr.Event, error) {
	if f.kind0Err != nil {
		return nil, f.kind0Err
	}
	result := make(map[string]*nostr.Event)
	remaining := make(map[string]bool, len(pubkeys))
	for _, pk := range pubkeys {
		remaining[pk] = true
	}
	for _, url := range relayURLs {
		for pk := range remaining {
			if ev, ok := f.kind0ByRelay[url][pk]; ok {
				result[pk] = ev
				delete(remaining, pk)
			}
		}
	}
	return result, nil
}

// fakeScanner is a strfryScanner test double: a fixed in-memory event list,
// so stats tests never touch a real strfry.
type fakeScanner struct {
	events []fakeStoredEvent
}

type fakeStoredEvent struct {
	Pubkey    string
	CreatedAt int64
}

func (f *fakeScanner) Count(ctx context.Context, filter map[string]any) (int, error) {
	authors, ok := filter["authors"].([]string)
	if !ok {
		return len(f.events), nil
	}
	set := make(map[string]bool, len(authors))
	for _, a := range authors {
		set[a] = true
	}
	n := 0
	for _, e := range f.events {
		if set[e.Pubkey] {
			n++
		}
	}
	return n, nil
}

func (f *fakeScanner) ScanAll(ctx context.Context, fn func(pubkey string, createdAt int64)) error {
	for _, e := range f.events {
		fn(e.Pubkey, e.CreatedAt)
	}
	return nil
}

// fakeReleaseChecker is a ReleaseChecker test double.
type fakeReleaseChecker struct {
	latest string
	err    error
}

func (f *fakeReleaseChecker) LatestRelease(ctx context.Context) (string, error) {
	return f.latest, f.err
}

const testOwner = "lord"

func newTestCycle(t *testing.T, fetcher NostrFetcher) *Cycle {
	t.Helper()
	return &Cycle{
		StateDir:       t.TempDir(),
		Owner:          testOwner,
		OwnRelay:       "ws://own",
		PublicRelays:   []string{"ws://pub1", "ws://pub2"},
		MaxInvites:     5,
		MaxDepth:       4,
		OuterTTLDays:   30,
		RunningVersion: "test",
		Fetcher:        fetcher,
		Scanner:        &fakeScanner{},
		ReleaseChecker: &fakeReleaseChecker{},
		Now:            func() time.Time { return time.Unix(1_000_000, 0) },
	}
}

func mustLedger(t *testing.T, c *Cycle) []Entry {
	t.Helper()
	entries, err := ReadLedger(c.ledgerPath())
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	return entries
}

func TestCycle_FollowsNeverShrinkOnFetchError(t *testing.T) {
	fetcher := &fakeFetcher{kind3Err: errors.New("relay unreachable")}
	c := newTestCycle(t, fetcher)

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
	c := newTestCycle(t, fetcher)

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
