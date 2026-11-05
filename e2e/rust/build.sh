#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Builds the Rust e2e runner against the Rust SDK in rust/ (a path dependency
# with the blocking feature). Needs a stable Rust toolchain. Takes no secrets.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cargo build --quiet --locked --release --manifest-path "$here/Cargo.toml"
