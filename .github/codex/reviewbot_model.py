# Adapted from Pavu a7b458e; modified for Speedle. Apache-2.0; see .github/codex/PAVU-LICENSE.txt.
"""Run ReviewBot against the configured provider without logging sensitive content."""

import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import time

import reviewbot_ark
import reviewbot_deepseek
from reviewbot_response import OUTPUT_TOKEN_LIMIT


DEFAULT_PROVIDER = "ark"
HTTP_BUDGET_SECONDS = 360
MAX_RESPONSE_BYTES = 16 * 1024 * 1024
MODEL_PATTERN = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}")
ARGUMENT_NAMES = (
    "input", "retry_input", "result", "request", "response",
    "system", "user", "retry_user",
)


class ConfigurationError(ValueError):
    pass


class ModelFailure(Exception):
    def __init__(self, reason):
        super().__init__(reason)
        self.reason = reason


class TransportError(ModelFailure):
    """A non-zero curl exit. HTTP errors and unusable output are not transport errors."""

    def __init__(self, curl_exit):
        super().__init__("transport_error")
        self.curl_exit = curl_exit


TRANSPORT_BACKOFF_SECONDS = (5, 15)
TRANSPORT_RETRY_RESERVE_SECONDS = 30
MAX_TRANSPORT_SENDS = 3


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


def provider_name(environment):
    value = environment.get("REVIEWBOT_PROVIDER", "").strip().lower()
    return value or DEFAULT_PROVIDER


def _ark_configuration(environment):
    configured_models = environment.get("ARK_REVIEW_MODELS", "")
    legacy_model = environment.get("ARK_REVIEW_MODEL", "").strip()
    model_names = configured_models.split()
    if not model_names and legacy_model:
        # Preserve the legacy override without changing the owner's default order.
        model_names = [legacy_model, *reviewbot_ark.DEFAULT_MODELS.split()]
    if not model_names:
        model_names = reviewbot_ark.DEFAULT_MODELS.split()
    models = list(dict.fromkeys(model_names))
    if not models or any(not MODEL_PATTERN.fullmatch(model) for model in models):
        raise ConfigurationError("ARK_REVIEW_MODELS")
    effort = (
        environment.get("ARK_REVIEW_REASONING_EFFORT", "").strip()
        or reviewbot_ark.DEFAULT_REASONING_EFFORT
    )
    if effort not in {"low", "medium", "high"}:
        raise ConfigurationError("ARK_REVIEW_REASONING_EFFORT")
    return {
        "provider": "ark",
        "api_key": environment.get("ARK_API_KEY", ""),
        "url": reviewbot_ark.URL,
        "models": models,
        "max_output_tokens": _integer_setting(
            environment,
            "ARK_REVIEW_MAX_OUTPUT_TOKENS",
            reviewbot_ark.DEFAULT_MAX_OUTPUT_TOKENS,
            1000000,
        ),
        "reasoning_effort": effort,
        "input_limit": _integer_setting(
            environment,
            "ARK_REVIEW_INPUT_LIMIT",
            reviewbot_ark.DEFAULT_INPUT_LIMIT,
            10000000,
        ),
        "build_payload": reviewbot_ark.payload,
        "extract": reviewbot_ark.extract,
    }


def _deepseek_configuration(environment):
    model = (
        environment.get("DEEPSEEK_REVIEW_MODEL", "").strip()
        or reviewbot_deepseek.DEFAULT_MODEL
    )
    if not MODEL_PATTERN.fullmatch(model):
        raise ConfigurationError("DEEPSEEK_REVIEW_MODEL")
    return {
        "provider": "deepseek",
        "api_key": environment.get("DEEPSEEK_API_KEY", ""),
        "url": reviewbot_deepseek.URL,
        "models": [model],
        "max_output_tokens": reviewbot_deepseek.DEFAULT_MAX_OUTPUT_TOKENS,
        "input_limit": reviewbot_deepseek.DEFAULT_INPUT_LIMIT,
        "build_payload": reviewbot_deepseek.payload,
        "extract": reviewbot_deepseek.extract,
    }


def configuration(environment):
    """Read all tunables here; workflow YAML only forwards repository variables."""
    provider = provider_name(environment)
    if provider == "ark":
        return _ark_configuration(environment)
    if provider == "deepseek":
        return _deepseek_configuration(environment)
    raise ConfigurationError("REVIEWBOT_PROVIDER")


def limited_input(path, limit):
    data = Path(path).read_bytes()
    if len(data) > limit:
        data = data[:limit] + (
            f"\n\n[Review input truncated at {limit} bytes. Report this as a validation gap.]\n"
        ).encode()
    return data.decode("utf-8", errors="replace")


def limited_messages(system_path, user_path, limit):
    """Cap the system and user message together, not each file separately."""
    system = Path(system_path).read_bytes()
    user = Path(user_path).read_bytes()
    if len(system) + len(user) > limit:
        notice = (
            f"\n\n[Review input truncated at {limit} bytes. Report this as a validation gap.]\n"
        ).encode()
        if len(system) >= limit:
            system = system[:limit] + notice
            user = b""
        else:
            user = user[:limit - len(system)] + notice
    return system.decode("utf-8", errors="replace"), user.decode("utf-8", errors="replace")


def http_post(request_path, response_path, timeout, url):
    """Post with curl's total deadline; keep credentials and response bodies out of logs."""
    authorization = json.dumps(f"Authorization: Bearer {os.environ['REVIEWBOT_API_KEY']}")
    try:
        completed = subprocess.run(
            [
                "curl", "--silent", "--show-error", "--connect-timeout", "20",
                "--max-time", str(timeout), "--output", str(response_path),
                "--write-out", "%{http_code}", url,
                # Read the bearer header from stdin so it never appears in curl's argv.
                "--config", "-",
                "-H", "Content-Type: application/json",
                "-H", "Accept: text/event-stream",
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
    if completed.returncode != 0:
        raise TransportError(completed.returncode)
    status_text = completed.stdout.strip()
    if not re.fullmatch(r"[0-9]{3}", status_text):
        raise ModelFailure("http_error")
    status = int(status_text)
    if 200 <= status <= 299 and Path(response_path).stat().st_size > MAX_RESPONSE_BYTES:
        raise ModelFailure("invalid_response")
    return status


def _inputs(config, paths):
    limit = config["input_limit"]
    if config["provider"] == "ark":
        return {
            "combined": [
                limited_input(paths["input"], limit),
                limited_input(paths["retry_input"], limit),
            ],
        }
    system, user = limited_messages(paths["system"], paths["user"], limit)
    _, retry_user = limited_messages(paths["system"], paths["retry_user"], limit)
    return {"system": system, "user": [user, retry_user]}


def _send_with_transport_retries(transport, paths, deadline, clock, sleeper, model):
    """Return the HTTP status, or None when this model attempt cannot continue.

    A non-zero curl exit resends the same request after 5s, then 15s, at most
    three times, and only while at least 30s of this model's budget would remain.
    """
    for send in range(1, MAX_TRANSPORT_SENDS + 1):
        remaining = math.ceil(deadline - clock())
        if remaining <= 0:
            print(f"ReviewBot reason_code=request_timeout model={model}", file=sys.stderr)
            return None
        try:
            return transport(paths["request"], paths["response"], remaining)
        except TransportError as failure:
            if send == MAX_TRANSPORT_SENDS:
                print(
                    "ReviewBot reason_code=retry_exhausted "
                    f"transport curl_exit={failure.curl_exit} sends={send} model={model}",
                    file=sys.stderr,
                )
                return None
            backoff = TRANSPORT_BACKOFF_SECONDS[send - 1]
            if deadline - clock() - backoff < TRANSPORT_RETRY_RESERVE_SECONDS:
                print(
                    "ReviewBot reason_code=request_timeout "
                    f"transport curl_exit={failure.curl_exit} sends={send} model={model}",
                    file=sys.stderr,
                )
                return None
            print(
                f"::notice::ReviewBot transport error (curl exit {failure.curl_exit}); "
                f"resending to the same model in {backoff}s."
            )
            sleeper(backoff)
        except ModelFailure as failure:
            print(f"ReviewBot reason_code={failure.reason} model={model}", file=sys.stderr)
            return None
    return None


def run(config, paths, transport=None, clock=time.monotonic, sleeper=time.sleep,
        budget_seconds=HTTP_BUDGET_SECONDS):
    """Try each model; a token-limit retry stays on that model and shares its budget."""
    destination = Path(paths["result"])
    destination.unlink(missing_ok=True)
    inputs = _inputs(config, paths)
    if transport is None:
        def default_transport(request_path, response_path, timeout):
            return http_post(request_path, response_path, timeout, config["url"])
        transport = default_transport
    for model_index, model in enumerate(config["models"]):
        deadline = clock() + budget_seconds
        retried = False
        for attempt in (0, 1):
            payload = config["build_payload"](model, config, inputs, attempt)
            Path(paths["request"]).write_text(
                json.dumps(payload, ensure_ascii=False), encoding="utf-8"
            )
            status = _send_with_transport_retries(
                transport, paths, deadline, clock, sleeper, model,
            )
            if status is None:
                break
            if not 200 <= status <= 299:
                print(
                    f"ReviewBot reason_code=http_error HTTP {status} model={model} "
                    "(response body omitted)",
                    file=sys.stderr,
                )
                break
            response_status = config["extract"](paths["response"], paths["result"], model)
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
    if len(arguments) != len(ARGUMENT_NAMES):
        print("ReviewBot reason_code=invalid_arguments", file=sys.stderr)
        return 2
    try:
        config = configuration(environment)
        if not config["api_key"]:
            raise ConfigurationError(
                "ARK_API_KEY" if config["provider"] == "ark" else "DEEPSEEK_API_KEY"
            )
        # http_post reads the key from the environment without ever printing it.
        os.environ["REVIEWBOT_API_KEY"] = config["api_key"]
        retried = run(config, dict(zip(ARGUMENT_NAMES, arguments)))
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
