package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sybenx/castle-for-strfry-experiment/internal/stateformat"
)

// TestRoundTrip_StewardWrittenStateThroughGatekeeperParser guards against
// fixture/writer drift (Phase 3a's acceptance criterion): steward and
// gatekeeper cannot import each other (both are `package main`), so this
// test writes banned.json/citizens.json using the exact same shared
// internal/stateformat types steward's ledger.go marshals
// (State.BannedJSON/State.CitizensJSON) and the exact same atomic
// write-then-rename steward uses (writeJSONAtomic in steward/main.go), then
// loads them through gatekeeper's real store/refresh/isBanned/isCitizen —
// the actual production parser, not a hand-rolled stand-in.
func TestRoundTrip_StewardWrittenStateThroughGatekeeperParser(t *testing.T) {
	dir := t.TempDir()

	banned := stateformat.Banned{Pubkeys: []string{pkBanned}}
	citizens := stateformat.Citizens{Pubkeys: []string{pkTreeCitizen, pkFollowCitizen, pkWard}}

	writeAtomicLikeSteward(t, filepath.Join(dir, "banned.json"), banned)
	writeAtomicLikeSteward(t, filepath.Join(dir, "citizens.json"), citizens)

	clock := newFakeClock()
	st := newStore(dir, time.Second, clock.now)
	st.refresh()

	if !st.isBanned(pkBanned) {
		t.Fatal("gatekeeper failed to parse a steward-written banned.json")
	}
	if st.isBanned(pkTreeCitizen) {
		t.Fatal("gatekeeper misread a citizen as banned")
	}
	for _, pk := range []string{pkTreeCitizen, pkFollowCitizen, pkWard} {
		if !st.isCitizen(pk) {
			t.Fatalf("gatekeeper failed to parse %s from a steward-written citizens.json", pk)
		}
	}
}

// writeAtomicLikeSteward mirrors steward/main.go's writeJSONAtomic (temp
// file in the same directory, then rename) byte-for-byte, so this test
// exercises the identical on-disk artifact steward produces rather than an
// approximation of it.
func writeAtomicLikeSteward(t *testing.T, path string, v interface{}) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		t.Fatal(err)
	}
}
