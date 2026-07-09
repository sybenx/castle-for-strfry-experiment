// Nostr client plumbing built on github.com/nbd-wtf/go-nostr: follows
// fetch, report fetch, reaction fetch, and single-event resolution. Lands
// in Phase 3a. See CLAUDE.md, "Cycle loop (every CYCLE_MINUTES)".
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

	// Reports returns every kind-1984 event authored by pubkey on
	// relayURL — the Lord's own relay only; CLAUDE.md's report intake
	// never mentions PUBLIC_RELAYS.
	Reports(ctx context.Context, relayURL, pubkey string) ([]*nostr.Event, error)

	// Reactions returns kind-7 events authored by pubkey on relayURL with
	// created_at > since.
	Reactions(ctx context.Context, relayURL, pubkey string, since int64) ([]*nostr.Event, error)

	// Event resolves a single event id, trying localRelay first and then
	// each of fallbackRelays in order, returning the first hit (nil if
	// none have it). The caller treats the result as a transient lookup —
	// nothing about the fetched event is stored, only its author read.
	Event(ctx context.Context, localRelay string, fallbackRelays []string, id string) (*nostr.Event, error)
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

func (relayFetcher) Reports(ctx context.Context, relayURL, pubkey string) ([]*nostr.Event, error) {
	filter := nostr.Filter{Kinds: []int{1984}, Authors: []string{pubkey}}
	events, err := queryRelay(ctx, relayURL, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: report fetch from %s: %v\n", relayURL, err)
		return nil, nil
	}
	return events, nil
}

func (relayFetcher) Reactions(ctx context.Context, relayURL, pubkey string, since int64) ([]*nostr.Event, error) {
	sinceTs := nostr.Timestamp(since)
	filter := nostr.Filter{Kinds: []int{7}, Authors: []string{pubkey}, Since: &sinceTs}
	events, err := queryRelay(ctx, relayURL, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: reaction fetch from %s: %v\n", relayURL, err)
		return nil, nil
	}
	return events, nil
}

func (relayFetcher) Event(ctx context.Context, localRelay string, fallbackRelays []string, id string) (*nostr.Event, error) {
	filter := nostr.Filter{IDs: []string{id}, Limit: 1}
	for _, url := range append([]string{localRelay}, fallbackRelays...) {
		if url == "" {
			continue
		}
		events, err := queryRelay(ctx, url, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "steward: event fetch %s from %s: %v\n", id, url, err)
			continue
		}
		if len(events) > 0 {
			return events[0], nil
		}
	}
	return nil, nil
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
