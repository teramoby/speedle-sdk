# Modified for Speedle. Apache-2.0; see .github/codex/PAVU-LICENSE.txt.
"""DeepSeek chat-completions requests and response extraction.

The endpoint is OpenAI-compatible and streaming. deepseek-flash has a
1M-token context window and a 384K-token maximum output; see
https://api-docs.deepseek.com/zh-cn/quick_start/pricing.
"""

import json
from pathlib import Path
import sys

from reviewbot_response import (
    OUTPUT_TOKEN_LIMIT,
    IncompleteStream,
    format_markdown,
    load_response,
)


URL = "https://api.deepseek.com/chat/completions"
DEFAULT_MODEL = "deepseek-flash"
# 65536 is the API's thinking-mode default and stays well inside 384K; together
# with the 750000-byte input cap it remains inside the 1M-token context.
DEFAULT_MAX_OUTPUT_TOKENS = 65536
DEFAULT_INPUT_LIMIT = 750000
FINISH_REASONS = {
    "stop", "length", "content_filter", "tool_calls",
    "insufficient_system_resource", "aborted",
}
INCOMPLETE_REASONS = {"content_filter", "insufficient_system_resource", "aborted"}


def payload(model, config, inputs, attempt):
    messages = []
    if inputs["system"]:
        messages.append({"role": "system", "content": inputs["system"]})
    messages.append({"role": "user", "content": inputs["user"][attempt]})
    return {
        "model": model,
        "messages": messages,
        "max_tokens": config["max_output_tokens"],
        "stream": True,
        "stream_options": {"include_usage": True},
    }


def label(value, allowed):
    if value is None:
        return "missing"
    return value if isinstance(value, str) and value in allowed else "other"


def diagnose(reason, response=None, choice=None):
    # All string labels are allowlisted. Never log content, reasoning, error
    # messages, headers, input, or JSON parser exceptions.
    response = response if isinstance(response, dict) else {}
    choice = choice if isinstance(choice, dict) else {}
    message = choice.get("message")
    message = message if isinstance(message, dict) else {}
    reasoning = message.get("reasoning_content")
    usage = response.get("usage")
    usage = usage if isinstance(usage, dict) else {}
    tokens = {}
    for key in ("prompt_tokens", "completion_tokens", "total_tokens"):
        value = usage.get(key)
        if type(value) is int and 0 <= value <= 10**12:
            tokens[key] = value
    output_details = usage.get("completion_tokens_details")
    if isinstance(output_details, dict):
        value = output_details.get("reasoning_tokens")
        if type(value) is int and 0 <= value <= 10**12:
            tokens["reasoning_tokens"] = value
    diagnostic = {
        "reason_code": reason,
        "finish_reason": label(choice.get("finish_reason"), FINISH_REASONS),
        "has_reasoning": isinstance(reasoning, str) and bool(reasoning.strip()),
        "usage": tokens,
    }
    print("ReviewBot response: " + json.dumps(diagnostic, separators=(",", ":")), file=sys.stderr)


def extract(response_path, result_path, requested_model):
    try:
        response = load_response(Path(response_path).read_text(), "deepseek")
    except IncompleteStream as stream:
        # No terminal finish_reason: never publish partial text, and never
        # treat the body as a transport error that the same request may be resent for.
        response = stream.snapshot if isinstance(stream.snapshot, dict) else {}
        choices = response.get("choices")
        choice = None
        if isinstance(choices, list) and choices and isinstance(choices[0], dict):
            choice = choices[0]
        diagnose("incomplete_stream", response, choice)
        return 1
    except (OSError, ValueError, RecursionError):
        diagnose("malformed_json")
        return 1
    if not isinstance(response, dict):
        diagnose("invalid_response")
        return 1
    if response.get("error"):
        diagnose("unsuccessful_response", response)
        return 1
    choices = response.get("choices")
    if not isinstance(choices, list) or not choices:
        diagnose("invalid_response", response)
        return 1
    choice = choices[0]
    if not isinstance(choice, dict):
        diagnose("invalid_response", response)
        return 1
    finish_reason = choice.get("finish_reason")
    if finish_reason == "length":
        diagnose("output_token_limit", response, choice)
        return OUTPUT_TOKEN_LIMIT
    if finish_reason in INCOMPLETE_REASONS:
        diagnose("incomplete_response", response, choice)
        return 11
    if finish_reason not in (None, "stop"):
        diagnose("invalid_response", response, choice)
        return 1
    message = choice.get("message")
    if not isinstance(message, dict):
        diagnose("invalid_response", response)
        return 1
    content = message.get("content")
    if isinstance(content, str) and content.strip():
        text = format_markdown(content)
        if text:
            model = response.get("model")
            if not isinstance(model, str) or not model.strip():
                model = requested_model
            Path(result_path).write_text(json.dumps({"message": text, "model": model}))
            return 0
    reasoning = message.get("reasoning_content")
    if isinstance(reasoning, str) and reasoning.strip():
        diagnose("reasoning_only", response, choice)
        return 12
    diagnose("empty_output", response, choice)
    return 1
