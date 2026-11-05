# Adapted from Pavu a7b458e; modified for Speedle+. Apache-2.0; see .github/codex/PAVU-LICENSE.txt.
"""Run ReviewBot against ordered Ark models without logging sensitive content."""

import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import time

from reviewbot_response import extract


ARK_URL = "https://ark.cn-beijing.volces.com/api/plan/v3/responses"
DEFAULT_MODELS = "kimi-k3.1-pro deepseek-v4.1-flash glm-5.3"
DEFAULT_MAX_OUTPUT_TOKENS = 32768
DEFAULT_REASONING_EFFORT = "medium"
DEFAULT_INPUT_LIMIT = 750000
HTTP_BUDGET_SECONDS = 360
MAX_RESPONSE_BYTES = 16 * 1024 * 1024
MODEL_PATTERN = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}")
OUTPUT_TOKEN_LIMIT = 10


class ConfigurationError(ValueError):
    pass


class ModelFailure(Exception):
    def __init__(self, reason):
        super().__init__(reason)
        self.reason = reason


def _integer_setting(environment, name, default, maximum):
    value = environment.get(name, "").strip()
    if not value:
        return default
    if not re.fullmatch(r"[1-9][0-9]*", value):
        raise ConfigurationError(name)
    number = int(value)
    if number > maximum:
        raise ConfigurationError(name)
    return number


def configuration(environment):
    """Read all tunables here; workflow YAML only forwards repository variables."""
    configured_models = environment.get("ARK_REVIEW_MODELS", "")
    legacy_model = environment.get("ARK_REVIEW_MODEL", "").strip()
    model_names = configured_models.split()
    if not model_names and legacy_model:
        # Preserve the legacy override without changing the owner's default order.
        model_names = [legacy_model, *DEFAULT_MODELS.split()]
    if not model_names:
        model_names = DEFAULT_MODELS.split()
    models = list(dict.fromkeys(model_names))
    if not models or any(not MODEL_PATTERN.fullmatch(model) for model in models):
        raise ConfigurationError("ARK_REVIEW_MODELS")
    effort = environment.get("ARK_REVIEW_REASONING_EFFORT", "").strip() or DEFAULT_REASONING_EFFORT
    if effort not in {"low", "medium", "high"}:
        raise ConfigurationError("ARK_REVIEW_REASONING_EFFORT")
    return {
        "models": models,
        "max_output_tokens": _integer_setting(
            environment, "ARK_REVIEW_MAX_OUTPUT_TOKENS", DEFAULT_MAX_OUTPUT_TOKENS, 1000000
        ),
        "reasoning_effort": effort,
        "input_limit": _integer_setting(
            environment, "ARK_REVIEW_INPUT_LIMIT", DEFAULT_INPUT_LIMIT, 10000000
        ),
        "api_key": environment.get("ARK_API_KEY", ""),
    }


def limited_input(path, limit):
    data = Path(path).read_bytes()
    if len(data) > limit:
        data = data[:limit] + (
            f"\n\n[Review input truncated at {limit} bytes. Report this as a validation gap.]\n"
        ).encode()
    return data.decode("utf-8", errors="replace")


def http_post(request_path, response_path, timeout):
    """Post with curl's total deadline; keep credentials and response bodies out of logs."""
    authorization = json.dumps(f"Authorization: Bearer {os.environ['ARK_API_KEY']}")
    try:
        completed = subprocess.run(
            [
                "curl", "--silent", "--show-error", "--connect-timeout", "20",
                "--max-time", str(timeout), "--output", str(response_path),
                "--write-out", "%{http_code}", ARK_URL,
                # Read the bearer header from stdin so it never appears in curl's argv.
                "--config", "-",
                "-H", "Content-Type: application/json",
                "--data-binary", f"@{request_path}",
            ],
            check=False,
            input=f"header = {authorization}\n",
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=timeout + 5,
        )
    except subprocess.TimeoutExpired as error:
        raise ModelFailure("request_timeout") from error
    except OSError as error:
        raise ModelFailure("transport_error") from error
    if completed.returncode == 28:
        raise ModelFailure("request_timeout")
    if completed.returncode != 0:
        raise ModelFailure("transport_error")
    status_text = completed.stdout.strip()
    if not re.fullmatch(r"[0-9]{3}", status_text):
        raise ModelFailure("http_error")
    status = int(status_text)
    if 200 <= status <= 299 and Path(response_path).stat().st_size > MAX_RESPONSE_BYTES:
        raise ModelFailure("invalid_response")
    return status


def run(config, input_path, retry_input_path, result_path, request_path, response_path,
        transport=http_post, clock=time.monotonic):
    """Try each model; a token-limit retry stays on that model and shares its budget."""
    destination = Path(result_path)
    destination.unlink(missing_ok=True)
    inputs = [
        limited_input(input_path, config["input_limit"]),
        limited_input(retry_input_path, config["input_limit"]),
    ]
    for model_index, model in enumerate(config["models"]):
        deadline = clock() + HTTP_BUDGET_SECONDS
        retried = False
        for attempt in (0, 1):
            remaining = math.ceil(deadline - clock())
            if remaining <= 0:
                failure = ModelFailure("request_timeout")
                print(f"ReviewBot reason_code={failure.reason} model={model}", file=sys.stderr)
                break
            payload = {
                "model": model,
                "input": inputs[attempt],
                "max_output_tokens": config["max_output_tokens"],
                "reasoning": {"effort": config["reasoning_effort"]},
            }
            Path(request_path).write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            try:
                status = transport(request_path, response_path, remaining)
            except ModelFailure as failure:
                print(f"ReviewBot reason_code={failure.reason} model={model}", file=sys.stderr)
                break
            if not 200 <= status <= 299:
                print(
                    f"ReviewBot reason_code=http_error HTTP {status} model={model} "
                    "(response body omitted)",
                    file=sys.stderr,
                )
                break
            response_status = extract(response_path, result_path, model)
            if response_status == 0:
                return retried
            if response_status == OUTPUT_TOKEN_LIMIT and attempt == 0:
                retried = True
                print(
                    f"::notice::ReviewBot output token limit on {model}; "
                    "retrying once with unified=3 diff context."
                )
                continue
            if response_status == OUTPUT_TOKEN_LIMIT:
                print(f"ReviewBot reason_code=retry_exhausted model={model}", file=sys.stderr)
            else:
                print(f"ReviewBot reason_code=unusable_output model={model}", file=sys.stderr)
            break
        if model_index + 1 < len(config["models"]):
            print(f"::warning::ReviewBot model {model} failed; trying the next model.")
        else:
            print(f"::warning::ReviewBot model {model} failed.")
    destination.unlink(missing_ok=True)
    print("ReviewBot reason_code=all_models_failed", file=sys.stderr)
    return None


def main(arguments, environment=os.environ):
    if len(arguments) != 5:
        print("ReviewBot reason_code=invalid_arguments", file=sys.stderr)
        return 2
    try:
        config = configuration(environment)
        if not config["api_key"]:
            raise ConfigurationError("ARK_API_KEY")
        # http_post reads the key from the environment without ever printing it.
        os.environ["ARK_API_KEY"] = config["api_key"]
        retried = run(config, *arguments)
    except (ConfigurationError, OSError, ValueError, RecursionError):
        print("ReviewBot reason_code=invalid_configuration", file=sys.stderr)
        return 1
    if retried is None:
        return 1
    output = environment.get("GITHUB_OUTPUT")
    if output:
        with Path(output).open("a", encoding="utf-8") as stream:
            stream.write(f"retried={'true' if retried else 'false'}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
