// Nostr client plumbing built on github.com/nbd-wtf/go-nostr: the
// follows-sync fetch (Phase 3a) and the kind-0 name-cache fetch (Phase 3b).
// See CLAUDE.md, "Cycle loop (every CYCLE_MINUTES)" and "Name cache and
// update banner".
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// relayQueryTimeout bounds each individual relay round-trip so one
// unreachable relay can't stall a whole cycle.
const relayQueryTimeout = 10 * time.Second

// NostrFetcher is every network read the cycle depends on. Interfaced so
// cycle tests can fake it without a live relay; relayFetcher below is the
// real go-nostr-backed implementation.
type NostrFetcher interface {
	// LatestKind3 returns the newest kind-3 event authored by pubkey across
	// relayURLs (CLAUDE.md's follows sync: "own relay + PUBLIC_RELAYS,
	// newest wins"), or nil if none of them have one. An unreachable relay
	// is logged and skipped, never allowed to sink the others; "on fetch
	// failure keep previous" is the caller's job, so this never returns an
	// error for anything short of a bad pubkey.
	LatestKind3(ctx context.Context, relayURLs []string, pubkey string) (*nostr.Event, error)

	// LatestKind0s returns the newest kind-0 event for each of pubkeys,
	// keyed by pubkey; a pubkey absent from the result had none anywhere. Per
	// CLAUDE.md's name cache ("local relay first, PUBLIC_RELAYS fallback"),
	// this tries relayURLs in the order given and only falls back to the
	// next relay for pubkeys still missing after the previous ones — unlike
	// LatestKind3's newest-wins merge across all relays at once.
	LatestKind0s(ctx context.Context, relayURLs []string, pubkeys []string) (map[string]*nostr.Event, error)
}

// relayFetcher is the real NostrFetcher, built on go-nostr's per-relay
// QuerySync. It holds no state of its own: every call connects, queries,
// and disconnects, so a dead relay never wedges a later cycle.
type relayFetcher struct{}

func (relayFetcher) LatestKind3(ctx context.Context, relayURLs []string, pubkey string) (*nostr.Event, error) {
	filter := nostr.Filter{Kinds: []int{3}, Authors: []string{pubkey}, Limit: 1}
	var newest *nostr.Event
	for _, url := range relayURLs {
		events, err := queryRelay(ctx, url, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "steward: kind-3 fetch from %s: %v\n", url, err)
			continue
		}
		for _, ev := range events {
			if newest == nil || ev.CreatedAt > newest.CreatedAt {
				newest = ev
			}
		}
	}
	return newest, nil
}

func (relayFetcher) LatestKind0s(ctx context.Context, relayURLs []string, pubkeys []string) (map[string]*nostr.Event, error) {
	result := make(map[string]*nostr.Event)
	remaining := pubkeys
	for _, url := range relayURLs {
		if len(remaining) == 0 {
			break
		}
		filter := nostr.Filter{Kinds: []int{0}, Authors: remaining}
		events, err := queryRelay(ctx, url, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "steward: kind-0 fetch from %s: %v\n", url, err)
			continue
		}
		for _, ev := range events {
			if cur, ok := result[ev.PubKey]; !ok || ev.CreatedAt > cur.CreatedAt {
				result[ev.PubKey] = ev
			}
		}
		var next []string
		for _, pk := range remaining {
			if _, ok := result[pk]; !ok {
				next = append(next, pk)
			}
		}
		remaining = next
	}
	return result, nil
}

// queryRelay connects to url, runs filter via QuerySync, and disconnects.
// Bounded by relayQueryTimeout so one slow relay can't stall a cycle.
func queryRelay(ctx context.Context, url string, filter nostr.Filter) ([]*nostr.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, relayQueryTimeout)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, url)
	if err != nil {
		return nil, err
	}
	defer relay.Close()

	return relay.QuerySync(ctx, filter)
}
