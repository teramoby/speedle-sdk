#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Builds the Go e2e runner against the Go SDK in go/ (a replace directive
# points at it) into e2e/go/.build. Needs Go 1.26 or later. Takes no secrets.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

go build -C "$here" -o "$here/.build/e2e" .
