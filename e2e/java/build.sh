#!/usr/bin/env bash
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
#
# Builds the Java SDK jar from java/ for the e2e runner. Needs Java 17 or
# later and Maven. Takes no secrets.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

mvn -B -ntp -q -f "$root/java/pom.xml" -DskipTests clean package
