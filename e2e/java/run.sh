#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Runs the e2e vectors with the Java SDK jar that build.sh made. Environment:
# see e2e/README.md.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

version="$(sed -n 's|^  <version>\(.*\)</version>$|\1|p' "$root/java/pom.xml" | head -n1)"
exec java -cp "$root/java/target/speedle-sdk-$version.jar" "$here/E2eRunner.java" "${SPEEDLE_E2E_VECTORS:-$root/e2e/vectors.json}"
