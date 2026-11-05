# Copied from teramoby/cloud.speedle.io ee9acab (#118 report handoff, #130 github_token key).
"""Seal a bounded review for the same-run publisher. Never log review/secret values."""

import json
import os
from pathlib import Path
import re
import sys


MAX_BYTES = 262144


def seal(result_path, artifact_path):
    destination = Path(artifact_path)
    # A failed retry must never leave a previously valid report to upload.
    try:
        destination.unlink(missing_ok=True)
        source = Path(result_path)
        if source.stat().st_size > MAX_BYTES:
            raise ValueError()
        result = json.loads(source.read_text(encoding="utf-8"))
        secrets = json.loads(os.environ["REVIEWBOT_SECRETS"])
        if not isinstance(secrets, dict) or not secrets:
            raise ValueError()
        secrets["GITHUB_TOKEN"] = os.environ["GH_TOKEN"]
        if not secrets.get("ARK_API_KEY") or not secrets["GITHUB_TOKEN"]:
            raise ValueError()
        if not isinstance(result, dict) or set(result) != {"message", "model"}:
            raise ValueError()
        message, model = result["message"], result["model"]
        if not isinstance(message, str) or not message.strip() or len(message.encode("utf-8")) > 240000:
            raise ValueError()
        if not isinstance(model, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}", model):
            raise ValueError()
        if os.environ["REVIEWBOT_RETRIED"] == "true":
            message += ("\n\n_Validation gap: ReviewBot retried after an output-token limit "
                        "with unified=3 diff context (initial unified=80)._")
        elif os.environ["REVIEWBOT_RETRIED"] != "false":
            raise ValueError()
        report = {"version": 1, "message": message, "model": model}
        for field, variable in (("run_id", "GITHUB_RUN_ID"), ("run_attempt", "GITHUB_RUN_ATTEMPT"),
                                ("pull_number", "PULL_NUMBER"), ("head_sha", "HEAD_SHA"),
                                ("command", "COMMAND")):
            report[field] = os.environ[variable]
        for field in ("run_id", "run_attempt", "pull_number"):
            if not re.fullmatch(r"[1-9][0-9]{0,15}", report[field]):
                raise ValueError()
        if not re.fullmatch(r"[a-f0-9]{40}", report["head_sha"]) or report["command"] not in {"review", "recheck", "explain"}:
            raise ValueError()
        # Inspect decoded fields AND serialized JSON. Escapes must not hide a
        # credential; no value, substring, digest or encoding is ever logged.
        payload = json.dumps(report, ensure_ascii=False)
        for name, secret in secrets.items():
            # GitHub injects lowercase github_token into toJSON(secrets).
            # All other names retain the strict repository-secret syntax.
            if (not isinstance(name, str)
                    or (name != "github_token" and not re.fullmatch(r"[A-Z][A-Z0-9_]*", name))
                    or not isinstance(secret, str)):
                raise ValueError()
            if secret and any(secret in text for text in [payload, *report.values()] if isinstance(text, str)):
                print(f"ReviewBot reason_code=secret_match secret_name={name}", file=sys.stderr)
                return 1
        if len(payload.encode("utf-8")) > MAX_BYTES:
            raise ValueError()
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(payload, encoding="utf-8")
        destination.chmod(0o600)
        return 0
    except (OSError, KeyError, ValueError, TypeError, RecursionError):
        print("ReviewBot reason_code=invalid_handoff", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(seal(*sys.argv[1:]))
