package main

import (
	"bufio"
	"bytes"
	"testing"
	"time"
)

// FuzzProcessLine exercises the strfry plugin protocol's stdin loop
// directly: arbitrary bytes must never panic processLine, regardless of
// how malformed the line is (CLAUDE.md: "A malformed input line must not
// kill the loop").
func FuzzProcessLine(f *testing.F) {
	seeds := []string{
		"",
		"not json at all",
		`{"type":"new","event":{"id":"abc","pubkey":"` + pkTreeCitizen + `","kind":1,"tags":[]},"sourceInfo":"1.2.3.4"}`,
		`{"type":"new","event":{"id":"abc","pubkey":"` + pkBanned + `","kind":1,"tags":[]},"sourceInfo":"1.2.3.4"}`,
		`{"type":"new","event":{"id":"abc","pubkey":"` + pkGiftWrapAuthor + `","kind":1059,"tags":[["p","` + pkTreeCitizen + `"]]},"sourceInfo":"1.2.3.4"}`,
		`{"type":"new","event":"not an object"}`,
		`{"type":"new"}`,
		`{}`,
		`null`,
		`[]`,
		`{"type":"new","event":{"tags":[["p"]]}}`,
		`{"type":"new","event":{"tags":[[]]}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	clock := newFakeClock()
	st := newStore(f.TempDir(), time.Second, clock.now)
	lim := newLimiter(rateLimitPerMinute, rateBurst, bucketIdleTTL, bucketSweepInterval, clock.now)

	f.Fuzz(func(t *testing.T, line string) {
		var out bytes.Buffer
		w := bufio.NewWriter(&out)
		processLine([]byte(line), st, lim, w)
	})
}
