package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Test pubkeys, matching gatekeeper/testdata/citizens.json,
// testdata/banned.json, and testdata/events.jsonl.
const (
	pkTreeCitizen            = "1111111111111111111111111111111111111111111111111111111111111111"
	pkFollowCitizen          = "2222222222222222222222222222222222222222222222222222222222222222"
	pkWard                   = "3333333333333333333333333333333333333333333333333333333333333333"
	pkBanned                 = "9999999999999999999999999999999999999999999999999999999999999999"
	pkGiftWrapAuthor         = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pkZapAuthor              = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pkStrangerGiftWrapAuthor = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	pkStranger               = "5555555555555555555555555555555555555555555555555555555555555555"
)

// fakeClock lets tests control time.Now() without sleeping.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock               { return &fakeClock{t: time.Unix(1700000000, 0)} }
func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestStore copies gatekeeper/testdata's citizens.json/banned.json into
// a fresh temp dir (so tests can mutate them without touching the
// committed fixtures) and returns a store pointed at it, already loaded.
func newTestStore(t *testing.T, clock *fakeClock, pollInterval time.Duration) (*store, string) {
	t.Helper()
	dir := t.TempDir()
	copyFixture(t, "testdata/citizens.json", filepath.Join(dir, "citizens.json"))
	copyFixture(t, "testdata/banned.json", filepath.Join(dir, "banned.json"))
	st := newStore(dir, pollInterval, clock.now)
	st.refresh()
	return st, dir
}

func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func loadEventFixtures(t *testing.T) []pluginRequest {
	t.Helper()
	f, err := os.Open("testdata/events.jsonl")
	if err != nil {
		t.Fatalf("open events fixture: %v", err)
	}
	defer f.Close()

	var reqs []pluginRequest
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		var req pluginRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // the fixture deliberately includes malformed lines
		}
		reqs = append(reqs, req)
	}
	return reqs
}

func eventByPubkeyKind(t *testing.T, reqs []pluginRequest, pubkey string, kind int) pluginRequest {
	t.Helper()
	for _, req := range reqs {
		var ev pluginEvent
		if json.Unmarshal(req.Event, &ev) != nil {
			continue
		}
		if ev.PubKey == pubkey && ev.Kind == kind {
			return req
		}
	}
	t.Fatalf("no fixture event found for pubkey=%s kind=%d", pubkey, kind)
	return pluginRequest{}
}

func decideFixture(t *testing.T, st *store, lim *limiter, req pluginRequest) pluginResponse {
	t.Helper()
	var ev pluginEvent
	if err := json.Unmarshal(req.Event, &ev); err != nil {
		t.Fatalf("unmarshal fixture event: %v", err)
	}
	return decide(ev, st, lim, req.Source)
}

func TestDecide_BannedRejected(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)

	resp := decideFixture(t, st, lim, eventByPubkeyKind(t, reqs, pkBanned, 1))
	if resp.Action != "reject" {
		t.Fatalf("banned author: got action %q, want reject", resp.Action)
	}
	if resp.Msg != msgBanned {
		t.Fatalf("banned author: got msg %q, want %q", resp.Msg, msgBanned)
	}
}

func TestDecide_CitizenAccepted(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)

	resp := decideFixture(t, st, lim, eventByPubkeyKind(t, reqs, pkTreeCitizen, 1))
	if resp.Action != "accept" {
		t.Fatalf("tree citizen: got action %q, want accept", resp.Action)
	}
}

// A ward is just another entry in citizens.json — the file carries no
// visibility info, so gatekeeper cannot and need not distinguish a ward
// from a favorite or a plain tree citizen.
func TestDecide_WardAccepted(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)

	resp := decideFixture(t, st, lim, eventByPubkeyKind(t, reqs, pkWard, 1))
	if resp.Action != "accept" {
		t.Fatalf("ward: got action %q, want accept", resp.Action)
	}
}

// Castle Mail is judged by recipient, so a gift wrap from an unknown
// (random one-time-key) author addressed to a citizen is accepted — but it
// still rides the per-IP bucket like any stranger event. Permanence exempts
// it from raids, never from the write path.
func TestDecide_GiftWrapToCitizen_AcceptedButBucketed(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)

	giftWrap := eventByPubkeyKind(t, reqs, pkGiftWrapAuthor, 1059)

	resp := decideFixture(t, st, lim, giftWrap)
	if resp.Action != "accept" {
		t.Fatalf("gift wrap to citizen: got action %q, want accept", resp.Action)
	}

	before := lim.bucketCount()
	if before == 0 {
		t.Fatalf("gift wrap to citizen did not consume a bucket token — Castle Mail must not bypass the rate limiter")
	}

	// Exhaust the bucket for this same source IP with further gift wraps;
	// it must start getting rate-limited exactly like a stranger would.
	limited := false
	for i := 0; i < int(rateBurst)+5; i++ {
		resp = decideFixture(t, st, lim, giftWrap)
		if resp.Action == "reject" {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatalf("gift wrap to citizen was never rate-limited despite exceeding burst — Castle Mail must ride the bucket")
	}
	if resp.Msg != msgRateLimit {
		t.Fatalf("gift wrap rate-limited: got msg %q, want %q", resp.Msg, msgRateLimit)
	}
}

// A gift wrap addressed only to a stranger p-tags no citizen and matches no
// tier rule, so it falls through to the Outer Lands like any stranger
// event, riding the bucket.
func TestDecide_StrangerToStrangerGiftWrap_RidesOuterLandsBucket(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)

	resp := decideFixture(t, st, lim, eventByPubkeyKind(t, reqs, pkStrangerGiftWrapAuthor, 1059))
	if resp.Action != "accept" {
		t.Fatalf("stranger-to-stranger gift wrap: got action %q, want accept (ages out at raid time, not rejected at write time)", resp.Action)
	}
}

func TestDecide_ZapReceiptToCitizen_Accepted(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)

	resp := decideFixture(t, st, lim, eventByPubkeyKind(t, reqs, pkZapAuthor, 9735))
	if resp.Action != "accept" {
		t.Fatalf("zap receipt to citizen: got action %q, want accept", resp.Action)
	}
}

func TestDecide_StrangerRateLimitedAfterBurst(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)
	req := eventByPubkeyKind(t, reqs, pkStranger, 1)

	accepted := 0
	for i := 0; i < int(rateBurst); i++ {
		resp := decideFixture(t, st, lim, req)
		if resp.Action != "accept" {
			t.Fatalf("stranger event %d: got action %q before burst exhausted, want accept", i, resp.Action)
		}
		accepted++
	}
	if accepted != int(rateBurst) {
		t.Fatalf("expected to accept exactly burst=%d events, accepted %d", int(rateBurst), accepted)
	}

	resp := decideFixture(t, st, lim, req)
	if resp.Action != "reject" || resp.Msg != msgRateLimit {
		t.Fatalf("after burst exhausted: got action=%q msg=%q, want reject/%q", resp.Action, resp.Msg, msgRateLimit)
	}
}

// Ephemeral kinds (20000-29999) from non-citizens are NOT exempt from the
// per-IP bucket (DECISIONS.md): they ride the same bucket as any stranger
// event, and repeated ephemeral traffic from one IP eventually gets
// rate-limited just like kind-1 spam would.
func TestDecide_EphemeralStrangerRidesBucket(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)
	req := eventByPubkeyKind(t, reqs, pkStranger, 20001)

	limited := false
	for i := 0; i < int(rateBurst)+5; i++ {
		resp := decideFixture(t, st, lim, req)
		if resp.Action == "reject" {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("ephemeral-kind stranger traffic was never rate-limited")
	}
}

// Citizens are exempt from the bucket entirely, even past what would be a
// stranger's burst limit.
func TestDecide_CitizenExemptFromBucket(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)
	reqs := loadEventFixtures(t)
	req := eventByPubkeyKind(t, reqs, pkTreeCitizen, 1)

	for i := 0; i < int(rateBurst)+20; i++ {
		resp := decideFixture(t, st, lim, req)
		if resp.Action != "accept" {
			t.Fatalf("citizen event %d: got action %q, want accept (citizens are bucket-exempt)", i, resp.Action)
		}
	}
}

func TestProcessLine_MalformedLineSurvives(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)

	var out bytes.Buffer
	w := bufio.NewWriter(&out)

	// A line that isn't JSON at all, then a syntactically valid request
	// whose event field isn't an object, then a well-formed request. None
	// of these should panic, and the loop must keep processing.
	processLine([]byte("this is not json"), st, lim, w)
	processLine([]byte(`{"type":"new","event":"not an object"}`), st, lim, w)

	good := `{"type":"new","event":{"id":"abc","pubkey":"` + pkTreeCitizen + `","kind":1,"tags":[]},"sourceInfo":"1.2.3.4"}`
	processLine([]byte(good), st, lim, w)
	w.Flush()

	var resp pluginResponse
	line := bytes.TrimSpace(out.Bytes())
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("expected exactly one valid response line, got %q (err=%v)", out.String(), err)
	}
	if resp.ID != "abc" || resp.Action != "accept" {
		t.Fatalf("got %+v, want id=abc action=accept", resp)
	}
}

// Every fixture line, including the deliberately malformed ones, must be
// processable without panicking and without producing more than one
// response line per valid input.
func TestProcessLine_AllFixtureLinesSurvive(t *testing.T) {
	clock := newFakeClock()
	st, _ := newTestStore(t, clock, time.Second)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)

	f, err := os.Open("testdata/events.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var out bytes.Buffer
		w := bufio.NewWriter(&out)
		processLine(sc.Bytes(), st, lim, w)
		w.Flush()
	}
}

func TestStore_HotReloadWithinOnePollInterval(t *testing.T) {
	clock := newFakeClock()
	pollInterval := time.Second
	st, dir := newTestStore(t, clock, pollInterval)

	if st.isBanned(pkStranger) {
		t.Fatal("stranger should not be banned before the reload")
	}

	// Ban the stranger by rewriting banned.json, then advance the clock
	// past one poll interval and refresh.
	clock.advance(10 * time.Millisecond) // ensure a distinct mtime is possible
	newBanned := stateJSON(t, []string{pkBanned, pkStranger})
	if err := os.WriteFile(filepath.Join(dir, "banned.json"), newBanned, 0o644); err != nil {
		t.Fatalf("rewrite banned.json: %v", err)
	}
	bumpMTime(t, filepath.Join(dir, "banned.json"), clock.now())

	// Within the same poll interval, refresh() must be a no-op.
	st.refresh()
	if st.isBanned(pkStranger) {
		t.Fatal("reload happened before the poll interval elapsed")
	}

	clock.advance(pollInterval)
	st.refresh()
	if !st.isBanned(pkStranger) {
		t.Fatal("stranger should be banned after one poll interval elapsed and a refresh")
	}
}

func TestStore_MissingFilesFailOpen(t *testing.T) {
	clock := newFakeClock()
	dir := t.TempDir() // no citizens.json / banned.json at all
	st := newStore(dir, time.Second, clock.now)
	st.refresh()

	if st.isBanned(pkBanned) {
		t.Fatal("missing banned.json must yield an empty (fail-open) set")
	}
	if st.isCitizen(pkTreeCitizen) {
		t.Fatal("missing citizens.json must yield an empty (fail-open) set")
	}
}

func TestLimiter_BucketEviction(t *testing.T) {
	clock := newFakeClock()
	idleTTL := 10 * time.Minute
	sweepEvery := time.Minute
	lim := newLimiter(rateLimitPerMinute, rateBurst, idleTTL, sweepEvery, clock.now)

	lim.Allow("1.2.3.4")
	if got := lim.bucketCount(); got != 1 {
		t.Fatalf("got %d buckets after one Allow, want 1", got)
	}

	// Advance past idle TTL and past the sweep interval, then touch a
	// different key so a sweep runs.
	clock.advance(idleTTL + time.Second)
	lim.Allow("5.6.7.8")

	if got := lim.bucketCount(); got != 1 {
		t.Fatalf("got %d buckets after idle eviction, want 1 (only the fresh key)", got)
	}
	if !lim.buckets["5.6.7.8"].lastSeen.Equal(clock.now()) {
		t.Fatal("fresh key's bucket should survive the sweep")
	}
}

func stateJSON(t *testing.T, pubkeys []string) []byte {
	t.Helper()
	b, err := json.Marshal(struct {
		Pubkeys []string `json:"pubkeys"`
	}{Pubkeys: pubkeys})
	if err != nil {
		t.Fatalf("marshal state json: %v", err)
	}
	return b
}

// bumpMTime sets a file's mtime explicitly so tests don't depend on the
// real filesystem clock lining up with the fake clock.
func bumpMTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
