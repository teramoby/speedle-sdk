#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Runs the e2e vectors with the .NET runner that build.sh built. Environment:
# see e2e/README.md.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

# The artifacts layout below bin/E2e/ depends on the SDK version, so find the
# one Release build instead of assuming its directory name.
dlls=()
while IFS= read -r dll; do dlls+=("$dll"); done < <(find "$here/.build/bin/E2e" -path '*release*' -name E2e.dll 2>/dev/null)
if [ "${#dlls[@]}" -ne 1 ]; then
  echo "dotnet e2e: expected one Release E2e.dll under $here/.build/bin/E2e, found ${#dlls[@]}; run build.sh" >&2
  exit 2
fi

export DOTNET_NOLOGO=1 DOTNET_CLI_TELEMETRY_OPTOUT=1
exec dotnet "${dlls[0]}" "${SPEEDLE_E2E_VECTORS:-$root/e2e/vectors.json}"
