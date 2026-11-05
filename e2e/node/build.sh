#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Installs and builds the Node.js SDK in node/ for the e2e runner. Needs
# Node.js 22 or later. Takes no secrets.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

npm --prefix "$root/node" ci --no-audit --no-fund --silent
npm --prefix "$root/node" run --silent build
