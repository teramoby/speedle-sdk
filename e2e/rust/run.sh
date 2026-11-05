#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Runs the e2e vectors with the Rust runner that build.sh built. Environment:
# see e2e/README.md.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

exec "$here/target/release/speedle-e2e" "${SPEEDLE_E2E_VECTORS:-$root/e2e/vectors.json}"
