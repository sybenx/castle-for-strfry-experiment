// The raid: streaming scan-then-delete of the Outer Lands past
// OUTER_TTL_DAYS. The only permitted strfry-delete call site. Scan/sweep
// logic lands in Phase 4.
// See CLAUDE.md, "The Raid".
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// strfryCLI is the interface to strfry's CLI, reached via `docker exec`
// into STRFRY_CONTAINER. strfry delete is the only irreversible operation
// in the system; ALL delete calls go through this one wrapper, with this
// file as the only call site (CLAUDE.md's "Delete confinement"). Interfaced
// so tests can fake it without a live strfry.
type strfryCLI interface {
	// DeleteByAuthors deletes every event authored by any of pubkeys,
	// batching at most deleteBatchSize per call. If dryRun, it logs the
	// batches it would run and deletes nothing. Returns the number of
	// pubkeys targeted.
	DeleteByAuthors(ctx context.Context, pubkeys []string, dryRun bool) (int, error)
}

const deleteBatchSize = 50

// strfryBinPath is where the reference dockurr/strfry image (the one
// deploy/docker-compose.yml and deploy/smoke.sh actually boot) puts the
// strfry binary: NOT on $PATH, so `docker exec <container> strfry ...`
// fails with "executable file not found" against a real container. Every
// `docker exec` invocation of strfry (here and in stats.go's scanner) uses
// this absolute path instead of bare "strfry".
const strfryBinPath = "/app/strfry"

// dockerStrfryCLI is the real strfryCLI, shelling out to
// `docker exec <container> strfry delete --filter ...`.
type dockerStrfryCLI struct {
	Container string
}

func (d *dockerStrfryCLI) DeleteByAuthors(ctx context.Context, pubkeys []string, dryRun bool) (int, error) {
	for _, batch := range chunkStrings(pubkeys, deleteBatchSize) {
		filter, err := json.Marshal(map[string]any{"authors": batch})
		if err != nil {
			return 0, err
		}
		if dryRun {
			fmt.Fprintf(os.Stderr, "steward: [dry-run] would delete %d authors: %s\n", len(batch), filter)
			continue
		}
		cmd := exec.CommandContext(ctx, "docker", "exec", d.Container, strfryBinPath, "delete", "--filter", string(filter))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("strfry delete: %w: %s", err, out)
		}
		fmt.Fprintf(os.Stderr, "steward: deleted %d authors' events\n", len(batch))
	}
	return len(pubkeys), nil
}

func chunkStrings(items []string, size int) [][]string {
	if size <= 0 || len(items) == 0 {
		return nil
	}
	var out [][]string
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[i:end])
	}
	return out
}
