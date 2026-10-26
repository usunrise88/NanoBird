#!/bin/sh
# Runs the official ACP test kit against nanobird acp (SPEC §12, gate G2). The kit is
# fetched at a pinned commit and installed from its own uv.lock. nanobird talks to a
# scripted model on localhost, so the run needs no engine and no key. See README.md.
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
GO=${GO:-go}
KIT_REPO=https://github.com/agentclientprotocol/acp-tck.git
KIT_REF=b7f40fb3c003c45926547be5a1f7e2b374035815
WORK=$ROOT/.acp-tck
PORT=${ACP_TCK_PORT:-18081}

mkdir -p "$WORK"
if [ ! -d "$WORK/kit/.git" ]; then
    git init -q "$WORK/kit"
    git -C "$WORK/kit" remote add origin "$KIT_REPO"
fi
git -C "$WORK/kit" fetch -q --depth 1 origin "$KIT_REF"
git -C "$WORK/kit" checkout -q --detach FETCH_HEAD

cd "$ROOT"
"$GO" build -o "$WORK/nanobird" ./cmd/nanobird
"$GO" build -o "$WORK/model" ./tools/acptck/model

AGENT_DIR=$(mktemp -d)
"$WORK/model" -addr "127.0.0.1:$PORT" &
MODEL_PID=$!
trap 'kill "$MODEL_PID" 2>/dev/null || true; rm -rf "$AGENT_DIR"' EXIT

tries=0
until curl -sf "http://127.0.0.1:$PORT/v1/models" >/dev/null; do
    tries=$((tries + 1))
    if [ "$tries" -ge 50 ]; then
        echo "the scripted model did not start on port $PORT" >&2
        exit 1
    fi
    sleep 0.2
done

cd "$WORK/kit"
uv run --locked acp-tck --agent-cwd "$AGENT_DIR" --timeout 60 --test-timeout 180 \
    --report-json "$WORK/report.json" -- "$WORK/nanobird" acp --endpoint "http://127.0.0.1:$PORT"
