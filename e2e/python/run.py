# Copyright (c) 2026 The Speedle Authors
# Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

"""Speedle SDK end-to-end runner for Python.

Runs every vector in e2e/vectors.json with ``Client.check`` and fails on any
mismatch. The contract is in e2e/README.md.
"""

from __future__ import annotations

import datetime
import json
import os
import sys
from typing import Any, NoReturn

import speedle


def optional(name: str) -> str | None:
    return os.environ.get(name) or None


def required(name: str) -> str:
    value = optional(name)
    if value is None:
        usage(f"{name} is required")
    return value


def usage(message: str) -> NoReturn:
    print(f"python e2e: {message}", file=sys.stderr)
    sys.exit(2)


def to_datetime(value: str) -> datetime.datetime:
    # Python 3.10 fromisoformat does not accept "Z".
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def attribute(item: dict[str, Any]) -> speedle.Attribute:
    name, kind, value = item["name"], item["type"], item["value"]
    if kind == "string":
        return speedle.Attribute.string(name, value)
    if kind == "numeric":
        return speedle.Attribute.numeric(name, value)
    if kind == "bool":
        return speedle.Attribute.boolean(name, value)
    if kind == "datetime":
        if isinstance(value, list):
            return speedle.Attribute.datetime(name, [to_datetime(v) for v in value])
        return speedle.Attribute.datetime(name, to_datetime(value))
    raise ValueError(f"unknown attribute type {kind}")


def fmt(allowed: bool, reason: int, error_message: str | None) -> str:
    s = f"{{allowed: {str(allowed).lower()}, reason: {reason}"
    if error_message is None:
        return s + "}"
    return s + f", errorMessage: {json.dumps(error_message, ensure_ascii=False)}}}"


def main() -> None:
    endpoint = required("SPEEDLE_E2E_ENDPOINT")
    service = required("SPEEDLE_E2E_SERVICE")
    backend = optional("SPEEDLE_E2E_BACKEND") or "speedle-cloud"
    token = optional("SPEEDLE_E2E_TOKEN")
    if backend not in ("speedle-cloud", "speedle-plus"):
        usage("SPEEDLE_E2E_BACKEND must be speedle-cloud or speedle-plus")
    if backend == "speedle-cloud" and not token:
        usage("SPEEDLE_E2E_TOKEN is required for speedle-cloud")
    if len(sys.argv) != 2:
        usage("usage: run.py VECTORS_JSON")

    with open(sys.argv[1], encoding="utf-8") as f:
        vectors = json.load(f)["vectors"]
    failed = 0
    try:
        client = speedle.Client(
            backend=backend,  # type: ignore[arg-type]
            endpoint=endpoint,
            auth=speedle.Auth.bearer(token) if token else speedle.Auth.none(),
            service_name=service,
            user_agent_suffix="speedle-sdk-e2e",
        )
    except speedle.SpeedleError as e:
        usage(f"{type(e).__name__}: {e}")
    with client:
        for vector in vectors:
            expect = vector.get("expect")
            want = fmt(expect["allowed"], expect["reason"], expect.get("errorMessage")) if expect else "no expectation"
            request = vector["request"]
            try:
                d = client.check(
                    [
                        speedle.Principal(name=p["name"], type=p["type"], idd=p.get("idd"))
                        for p in request["subject"]["principals"]
                    ],
                    request["action"],
                    request["resource"],
                    [attribute(a) for a in request.get("attributes", [])],
                )
                got = fmt(d.allowed, int(d.reason), d.error_message)
            except speedle.SpeedleError as e:
                got = f"{type(e).__name__}: {e}"
            if want == got:
                print(f"PASS {vector['name']}")
            else:
                failed += 1
                print(f"FAIL {vector['name']}: want {want}, got {got}")
    print(f"python: {len(vectors) - failed} passed, {failed} failed against {endpoint}")
    sys.exit(0 if failed == 0 and vectors else 1)


if __name__ == "__main__":
    main()
