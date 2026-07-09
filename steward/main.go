// Command steward is the Castle's sidecar daemon: follows sync, report
// intake, raids, the invite tree, elevation, manual archival, the signed
// HTTP API, NIP-05 serving, and stats. It also serves towncrier's static
// files, so there is no separate web container. See CLAUDE.md, Component 2.
//
// As of Phase 3a this runs the cycle loop (follows sync, report intake,
// react-warding, ledger merge, purge-newly-banned). The HTTP API, raid, and
// scribe land in Phases 4-5.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// config holds the env-driven settings from CLAUDE.md, Component 2.
type config struct {
	OwnerPubkey     string
	StrfryContainer string
	PublicRelays    []string
	OuterTTLDays    int
	CycleMinutes    int
	RaidCron        string
	RaidDryRun      bool
	MaxInvites      int
	MaxDepth        int
	Nip05Domain     string
	Listen          string
}

func loadConfig() (config, error) {
	return config{
		OwnerPubkey:     os.Getenv("OWNER_PUBKEY"),
		StrfryContainer: envOr("STRFRY_CONTAINER", "strfry"),
		PublicRelays:    envList("PUBLIC_RELAYS"),
		OuterTTLDays:    envInt("OUTER_TTL_DAYS", 30),
		CycleMinutes:    envInt("CYCLE_MINUTES", 10),
		RaidCron:        os.Getenv("RAID_CRON"),
		RaidDryRun:      envBool("RAID_DRY_RUN", true),
		MaxInvites:      envInt("MAX_INVITES", 5),
		MaxDepth:        envInt("MAX_DEPTH", 4),
		Nip05Domain:     os.Getenv("NIP05_DOMAIN"),
		Listen:          envOr("LISTEN", ":8787"),
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envList splits a comma-separated env var, trimming whitespace and
// dropping empty elements (so PUBLIC_RELAYS="" yields nil, not [""]).
func envList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// envInt reads an integer env var, falling back to def if unset or
// unparseable (a malformed knob must not crash steward at startup).
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: invalid %s=%q, using default %d: %v\n", key, v, def, err)
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: invalid %s=%q, using default %v: %v\n", key, v, def, err)
		return def
	}
	return b
}

// ownRelayURL is steward's local websocket address for STRFRY_CONTAINER.
// Not a separate env var: steward and gatekeeper already share the same
// trust source for "which strfry" (STRFRY_CONTAINER is also the docker-exec
// target), and strfry listens on 7777 on the compose network by convention
// (see deploy/docker-compose.yml). One knob, not two.
func ownRelayURL(container string) string {
	return fmt.Sprintf("ws://%s:7777", container)
}

// writeJSONAtomic marshals v and writes it to path via a temp file in the
// same directory followed by a rename, so a crash mid-write never leaves a
// truncated state file for gatekeeper (or another reader) to hot-reload.
func writeJSONAtomic(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "steward: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cycle := NewCycle(cfg, relayFetcher{}, &dockerStrfryCLI{Container: cfg.StrfryContainer})

	runCycle := func() {
		if err := cycle.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "steward: cycle failed: %v\n", err)
		}
	}

	runCycle()

	ticker := time.NewTicker(time.Duration(cfg.CycleMinutes) * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCycle()
		}
	}
}
