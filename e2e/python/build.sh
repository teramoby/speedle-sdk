#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Creates the locked Python SDK environment of python/ (without dev
# dependencies) for the e2e runner. Needs uv. Takes no secrets.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

uv sync --quiet --project "$root/python" --locked --no-dev
