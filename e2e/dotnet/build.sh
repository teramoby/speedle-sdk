#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Builds the .NET e2e runner and the C# SDK from dotnet/ (a project
# reference) into e2e/dotnet/.build, so nothing is written into dotnet/.
# Needs the .NET 8 SDK or later. Takes no secrets.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export DOTNET_NOLOGO=1 DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1
dotnet build "$here/E2e.csproj" -c Release --artifacts-path "$here/.build" -nologo -v quiet
