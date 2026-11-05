#!/usr/bin/env python3
# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.
"""Check that a release tag matches the SDK's package metadata.

Usage: release-version.py TAG

TAG is `<language>/vX.Y.Z`, for example `node/v0.1.0`. The script prints the
language and version as `language=...` and `version=...` lines (for
$GITHUB_OUTPUT) and exits non-zero when the tag is malformed or any version
source of that SDK differs from X.Y.Z. Run it from the repository root.
"""

from __future__ import annotations

import json
import re
import sys
import tomllib
import xml.etree.ElementTree as ET
from pathlib import Path

TAG = re.compile(r"^(node|python|java|go|dotnet|rust)/v((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$")
POM_NS = {"m": "http://maven.apache.org/POM/4.0.0"}


def regex(path: str, pattern: str) -> str:
    match = re.search(pattern, Path(path).read_text(encoding="utf-8"), re.MULTILINE)
    if not match:
        raise SystemExit(f"error: no version found in {path} (pattern {pattern!r})")
    return match.group(1)


def node() -> dict[str, str]:
    package = json.loads(Path("node/package.json").read_text(encoding="utf-8"))
    lock = json.loads(Path("node/package-lock.json").read_text(encoding="utf-8"))
    return {
        "node/package.json version": package["version"],
        "node/package-lock.json version": lock["version"],
        'node/package-lock.json packages[""].version': lock["packages"][""]["version"],
        "node/src/version.ts VERSION": regex("node/src/version.ts", r'^export const VERSION = "([^"]+)";'),
    }


def python() -> dict[str, str]:
    project = tomllib.loads(Path("python/pyproject.toml").read_text(encoding="utf-8"))["project"]
    lock = tomllib.loads(Path("python/uv.lock").read_text(encoding="utf-8"))
    locked = [p["version"] for p in lock.get("package", []) if p.get("name") == project["name"]]
    return {
        "python/pyproject.toml project.version": project["version"],
        "python/src/speedle/_version.py __version__": regex(
            "python/src/speedle/_version.py", r'^__version__ = "([^"]+)"'
        ),
        "python/uv.lock speedle version": locked[0] if len(locked) == 1 else f"<{len(locked)} entries>",
    }


def java() -> dict[str, str]:
    version = ET.parse("java/pom.xml").getroot().findtext("m:version", namespaces=POM_NS)
    return {"java/pom.xml project.version": version or "<missing>"}


def go() -> dict[str, str]:
    return {"go/speedle.go Version": regex("go/speedle.go", r'^const Version = "([^"]+)"')}


def dotnet() -> dict[str, str]:
    csproj = "dotnet/src/Speedle.Sdk/Speedle.Sdk.csproj"
    version = ET.parse(csproj).getroot().findtext("PropertyGroup/Version")
    return {f"{csproj} Version": version or "<missing>"}


def rust() -> dict[str, str]:
    manifest = tomllib.loads(Path("rust/Cargo.toml").read_text(encoding="utf-8"))
    lock = tomllib.loads(Path("rust/Cargo.lock").read_text(encoding="utf-8"))
    locked = [p["version"] for p in lock.get("package", []) if p.get("name") == manifest["package"]["name"]]
    return {
        "rust/Cargo.toml package.version": manifest["package"]["version"],
        "rust/Cargo.lock speedle version": locked[0] if len(locked) == 1 else f"<{len(locked)} entries>",
    }


SOURCES = {"node": node, "python": python, "java": java, "go": go, "dotnet": dotnet, "rust": rust}


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    match = TAG.fullmatch(argv[1])
    if not match:
        print(f"error: tag {argv[1]!r} is not <language>/vX.Y.Z with language in {sorted(SOURCES)}", file=sys.stderr)
        return 1
    language, version = match.group(1), match.group(2)
    mismatches = 0
    for source, found in SOURCES[language]().items():
        ok = found == version
        mismatches += not ok
        print(f"{'ok' if ok else 'MISMATCH'}: {source} = {found}", file=sys.stderr)
    if mismatches:
        print(f"error: {mismatches} version source(s) differ from tag version {version}", file=sys.stderr)
        return 1
    print(f"language={language}")
    print(f"version={version}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
