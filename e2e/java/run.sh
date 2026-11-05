#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Runs the e2e vectors with the Java SDK jar that build.sh made. Environment:
# see e2e/README.md.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

jars=()
for jar in "$root"/java/target/speedle-sdk-*.jar; do
  case "$jar" in *-sources.jar | *-javadoc.jar | *"*"*) ;; *) jars+=("$jar") ;; esac
done
if [ "${#jars[@]}" -ne 1 ]; then
  echo "java e2e: expected one SDK jar in $root/java/target, found ${#jars[@]}; run build.sh" >&2
  exit 2
fi
exec java -cp "${jars[0]}" "$here/E2eRunner.java" "${SPEEDLE_E2E_VECTORS:-$root/e2e/vectors.json}"
