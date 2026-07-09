#!/usr/bin/env bash
# `make smoke`: scratch strfry + fixture events via nak, exercising the real
# gatekeeper binary (Phase 1) and a real steward cycle (Phase 3a) end to
# end. See PLAN.md's Phase 3a acceptance criterion: "cycle runs against a
# scratch strfry in docker-compose with fixture events published via nak;
# banned.json/citizens.json/tree.json come out correct."
#
# Deliberately does NOT shell out to `docker compose` — it isn't guaranteed
# present (the Docker CLI and the compose plugin are installed separately;
# this environment has the former without the latter), and the scratch
# stack's needs (deterministic container/network names, precise mount
# ordering) are simpler to get right with plain `docker` commands anyway.
# docker-compose.yml remains the documented reference for wiring the Castle
# into a real Portainer stack; it is not this script's dependency.
#
# Colima/virtiofs gotcha (see .claude/notes/phase1.md): bind-mounting a
# single host FILE into a fresh container races virtiofs's file-visibility
# propagation and can silently vivify the mount point as an empty
# directory. Every bind mount below is a whole DIRECTORY (populated on the
# host before the mount happens), never a single file, to sidestep this
# entirely.
set -euo pipefail
cd "$(dirname "$0")"
REPO_ROOT="$(cd .. && pwd)"

for bin in docker nak jq; do
	if ! command -v "$bin" >/dev/null 2>&1; then
		echo "==> smoke: $bin is required but not found in PATH" >&2
		exit 1
	fi
done

NETWORK=castle-smoke
VOLUME_STATE=castle-state
VOLUME_DB=castle-smoke-db

case "$(uname -m)" in
	arm64|aarch64) ARCH=arm64 ;;
	x86_64|amd64)  ARCH=amd64 ;;
	*) echo "==> smoke: unsupported host arch $(uname -m)" >&2; exit 1 ;;
esac
BIN_DIR="$REPO_ROOT/bin/linux-$ARCH"
for f in gatekeeper steward; do
	if [ ! -x "$BIN_DIR/$f" ]; then
		echo "==> smoke: $BIN_DIR/$f missing — run 'make build' first" >&2
		exit 1
	fi
done

cleanup() {
	echo "==> smoke: tearing down"
	docker rm -f castle-smoke-steward castle-smoke-strfry >/dev/null 2>&1 || true
	docker network rm "$NETWORK" >/dev/null 2>&1 || true
	docker volume rm "$VOLUME_STATE" "$VOLUME_DB" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup >/dev/null 2>&1 || true # in case a previous run left something behind

echo "==> smoke: pre-seeding the gatekeeper binary into the castle-state volume"
docker network create "$NETWORK" >/dev/null
docker volume create "$VOLUME_STATE" >/dev/null
docker volume create "$VOLUME_DB" >/dev/null
docker run --rm \
	-v "$VOLUME_STATE":/plugin \
	-v "$BIN_DIR":/src:ro \
	alpine:3 sh -c 'cp /src/gatekeeper /plugin/gatekeeper && chmod +x /plugin/gatekeeper'

echo "==> smoke: bringing up scratch strfry"
docker run -d --name castle-smoke-strfry \
	--network "$NETWORK" \
	-p 7777:7777 \
	-v "$REPO_ROOT/deploy/smoke-conf":/config:ro \
	-v "$VOLUME_DB":/app/strfry-db \
	-v "$VOLUME_STATE":/plugin \
	-e STRFRY_CONFIG=/config/strfry.conf \
	-e MAIL_RATE_PER_MIN=10 \
	-e LANDS_RATE_PER_MIN=0 \
	dockurr/strfry:latest >/dev/null

echo "==> smoke: waiting for strfry to accept connections"
up=false
for i in $(seq 1 30); do
	if nak req -l 1 ws://localhost:7777 >/dev/null 2>&1; then
		up=true
		break
	fi
	sleep 1
done
if [ "$up" != true ]; then
	echo "==> smoke: strfry never came up" >&2
	docker logs castle-smoke-strfry || true
	exit 1
fi
echo "==> smoke: strfry is up"

echo "==> smoke: generating fixture keys"
OWNER_SEC=$(nak key generate); OWNER_PUB=$(nak key public "$OWNER_SEC")
FOLLOW_SEC=$(nak key generate); FOLLOW_PUB=$(nak key public "$FOLLOW_SEC")
SPAM_SEC=$(nak key generate); SPAM_PUB=$(nak key public "$SPAM_SEC")
NOTEE_SEC=$(nak key generate); NOTEE_PUB=$(nak key public "$NOTEE_SEC")
echo "    owner=$OWNER_PUB follow=$FOLLOW_PUB spammer=$SPAM_PUB notee=$NOTEE_PUB"

echo "==> smoke: publishing fixture events"
# The Lord follows FOLLOW_PUB (kind 3).
nak event -k 3 -p "$FOLLOW_PUB" --sec "$OWNER_SEC" -q ws://localhost:7777 >/dev/null
# A stranger's note for the Lord to react to (react-warding target).
NOTE_ID=$(nak event -k 1 -c "a note for the Lord to react to" --sec "$NOTEE_SEC" -q ws://localhost:7777 | jq -r .id)
# The Lord reports SPAM_PUB as spam (kind 1984).
nak event -k 1984 -t p="$SPAM_PUB;spam" -c "reported for spam" --sec "$OWNER_SEC" -q ws://localhost:7777 >/dev/null
# The Lord reacts to the stranger's note (kind 7, react-warding).
nak event -k 7 -e "$NOTE_ID" -c "+" --sec "$OWNER_SEC" -q ws://localhost:7777 >/dev/null

# STRFRY_CONTAINER doubles as the websocket hostname (ownRelayURL) and the
# `docker exec` purge target; the container's own name resolves fine as
# both on this user-defined network. The steward image here has no docker
# CLI or socket, so the purge-newly-banned step's `docker exec` will fail
# and log to stderr — harmless for what this test asserts (banned.json,
# citizens.json, and enforcement all come from state files, not from the
# purge). Exercising the real docker-exec delete path is Phase 4's job.
echo "==> smoke: running one steward cycle"
docker run -d --name castle-smoke-steward \
	--network "$NETWORK" \
	-v "$VOLUME_STATE":/state \
	-v "$BIN_DIR":/bin/castle:ro \
	-e OWNER_PUBKEY="$OWNER_PUB" \
	-e STRFRY_CONTAINER=castle-smoke-strfry \
	-e PUBLIC_RELAYS= \
	-e CYCLE_MINUTES=60 \
	--entrypoint /bin/castle/steward \
	alpine:3 >/dev/null
sleep 8
docker stop castle-smoke-steward >/dev/null
docker logs castle-smoke-steward 2>&1 | sed 's/^/    steward: /' || true
docker rm castle-smoke-steward >/dev/null

echo "==> smoke: asserting cycle output"
STATE=$(docker run --rm -v "$VOLUME_STATE":/state alpine:3 sh -c \
	'echo ---banned---; cat /state/banned.json 2>/dev/null || echo "{}"; echo; \
	 echo ---citizens---; cat /state/citizens.json 2>/dev/null || echo "{}"; echo; \
	 echo ---tree---; cat /state/tree.json 2>/dev/null || echo "{}"; echo; \
	 echo ---ledger---; cat /state/ledger.jsonl 2>/dev/null || true')
echo "$STATE"

BANNED_JSON=$(echo "$STATE" | sed -n '/^---banned---$/,/^---citizens---$/p' | sed '1d;$d')
CITIZENS_JSON=$(echo "$STATE" | sed -n '/^---citizens---$/,/^---tree---$/p' | sed '1d;$d')
LEDGER=$(echo "$STATE" | sed -n '/^---ledger---$/,$p' | sed '1d')

fail=0
if ! echo "$BANNED_JSON" | jq -e --arg pk "$SPAM_PUB" '.pubkeys | index($pk)' >/dev/null; then
	echo "==> smoke: FAIL — banned.json missing the reported spammer" >&2
	fail=1
fi
if ! echo "$CITIZENS_JSON" | jq -e --arg pk "$FOLLOW_PUB" '.pubkeys | index($pk)' >/dev/null; then
	echo "==> smoke: FAIL — citizens.json missing the Lord's follow" >&2
	fail=1
fi
if ! echo "$LEDGER" | jq -e --arg pk "$NOTEE_PUB" 'select(.verb == "elevate" and .pubkey == $pk and (.public // false) == false)' >/dev/null; then
	echo "==> smoke: FAIL — ledger missing the react-ward elevate entry for the reacted author" >&2
	fail=1
fi
if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "==> smoke: banned.json, citizens.json, and the ledger all reflect the real cycle correctly"

echo "==> smoke: confirming gatekeeper enforces the fresh ban against a live event"
enforced=false
for i in $(seq 1 10); do
	if nak event -k 1 -c "still trying to post" --sec "$SPAM_SEC" ws://localhost:7777 2>&1 | grep -qi "exiled"; then
		enforced=true
		break
	fi
	sleep 1
done
if [ "$enforced" != true ]; then
	echo "==> smoke: FAIL — gatekeeper never rejected the newly-banned pubkey" >&2
	exit 1
fi
echo "==> smoke: gatekeeper rejected the newly-banned pubkey with the themed exile message"

echo "==> smoke: all checks passed"
