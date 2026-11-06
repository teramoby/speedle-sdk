#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Local proof for the SDK end-to-end tests: builds self-hosted Speedle PMS, ADS and spctl
# from a self-hosted Speedle checkout, loads e2e/policies.json through PMS with spctl,
# checks the parsed forms, starts a REST ADS on loopback and checks every
# vector in e2e/vectors.json with raw HTTP. Then it runs the SDK runners named
# on the command line against the same ADS.
#
# Usage: SPEEDLE_PLUS_DIR=/path/to/speedle-plus e2e/speedle-plus/run-local.sh [--record] [all | LANG ...]
#   --record  write the self-hosted Speedle parsed forms into policies.json and the
#             self-hosted Speedle decisions into vectors.json instead of checking them
#   LANG      java, node, python, go, dotnet or rust
# Environment: E2E_PMS_PORT (default 16733), E2E_ADS_PORT (default 16734).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
e2e="$(dirname "$here")"
all_langs=(java node python go dotnet rust)

record=0
langs=()
for arg in "$@"; do
  case "$arg" in
    --record) record=1 ;;
    all) langs=("${all_langs[@]}") ;;
    java | node | python | go | dotnet | rust) langs+=("$arg") ;;
    *)
      echo "usage: SPEEDLE_PLUS_DIR=DIR $0 [--record] [all | java | node | python | go | dotnet | rust ...]" >&2
      exit 2
      ;;
  esac
done
: "${SPEEDLE_PLUS_DIR:?set SPEEDLE_PLUS_DIR to a Speedle+ checkout}"
pms_port="${E2E_PMS_PORT:-16733}"
ads_port="${E2E_ADS_PORT:-16734}"
service=sdk-e2e

work="$(mktemp -d "${TMPDIR:-/tmp}/speedle-e2e.XXXXXX")"
pids=()
cleanup() {
  for pid in ${pids[@]+"${pids[@]}"}; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# wait_up URL PID LOG: waits until URL answers any HTTP status, from PID.
wait_up() {
  for _ in $(seq 100); do
    if ! kill -0 "$2" 2>/dev/null; then
      echo "server exited; log:" >&2
      cat "$3" >&2
      exit 1
    fi
    code="$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$1" || true)"
    [ "$code" != 000 ] && return 0
    sleep 0.1
  done
  echo "timed out waiting for $1; log:" >&2
  cat "$3" >&2
  exit 1
}

# Refuse to start if something already listens, so we never test another server.
for port in "$pms_port" "$ads_port"; do
  if [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/" || true)" != 000 ]; then
    echo "port $port is in use; set E2E_PMS_PORT or E2E_ADS_PORT" >&2
    exit 1
  fi
done

commit="$(git -C "$SPEEDLE_PLUS_DIR" rev-parse HEAD)"
if [ "$record" = 1 ] && [ -n "$(git -C "$SPEEDLE_PLUS_DIR" status --porcelain)" ]; then
  echo "refusing to record: $SPEEDLE_PLUS_DIR has uncommitted changes, so $commit would not describe the build" >&2
  exit 1
fi
echo "== building Speedle+ $commit from $SPEEDLE_PLUS_DIR"
go build -C "$SPEEDLE_PLUS_DIR" -mod=readonly -trimpath -o "$work/bin/" \
  ./cmd/speedle-pms ./cmd/speedle-ads ./cmd/spctl
go build -C "$here" -o "$work/bin/e2e-speedle-plus" .
tool="$work/bin/e2e-speedle-plus"

echo "== loading $e2e/policies.json through PMS with spctl"
printf '{}\n' >"$work/store.json"
"$work/bin/speedle-pms" --store-type file --filestore-loc "$work/store.json" \
  --endpoint "127.0.0.1:$pms_port" --grpc-endpoint 127.0.0.1:0 --insecure=true \
  >"$work/pms.log" 2>&1 &
pms_pid=$!
pids+=("$pms_pid")
wait_up "http://127.0.0.1:$pms_port/policy-mgmt/v1/service" "$pms_pid" "$work/pms.log"
"$tool" load -spctl "$work/bin/spctl" -pms "http://127.0.0.1:$pms_port/policy-mgmt/v1/" \
  -service "$service" -policies "$e2e/policies.json"
kill "$pms_pid"
wait "$pms_pid" 2>/dev/null || true

write=()
[ "$record" = 1 ] && write=(-write)
"$tool" parsed -store "$work/store.json" -service "$service" -policies "$e2e/policies.json" ${write[@]+"${write[@]}"}

echo "== checking $e2e/vectors.json against Speedle+ ADS with raw HTTP"
"$work/bin/speedle-ads" --store-type file --filestore-loc "$work/store.json" \
  --endpoint "127.0.0.1:$ads_port" --grpc-endpoint 127.0.0.1:0 --insecure=true \
  >"$work/ads.log" 2>&1 &
ads_pid=$!
pids+=("$ads_pid")
wait_up "http://127.0.0.1:$ads_port/" "$ads_pid" "$work/ads.log"
if [ "$record" = 1 ]; then
  "$tool" vectors -endpoint "http://127.0.0.1:$ads_port" -service "$service" \
    -vectors "$e2e/vectors.json" -write -commit "$commit"
else
  "$tool" vectors -endpoint "http://127.0.0.1:$ads_port" -service "$service" -vectors "$e2e/vectors.json"
fi

failed=()
for lang in ${langs[@]+"${langs[@]}"}; do
  echo "== $lang SDK against Speedle+ ADS"
  # The local ADS takes no token; never pass on one from the caller's shell.
  if ! "$e2e/$lang/build.sh" || ! env -u SPEEDLE_E2E_TOKEN SPEEDLE_E2E_BACKEND=speedle-plus \
    SPEEDLE_E2E_ENDPOINT="http://127.0.0.1:$ads_port" \
    SPEEDLE_E2E_SERVICE="$service" \
    SPEEDLE_E2E_VECTORS="$e2e/vectors.json" \
    "$e2e/$lang/run.sh"; then
    failed+=("$lang")
  fi
done
if [ "${#failed[@]}" -gt 0 ]; then
  echo "FAILED: ${failed[*]}" >&2
  exit 1
fi
echo "== OK"
