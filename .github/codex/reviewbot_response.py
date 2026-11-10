# Adapted from Pavu a7b458e; modified for Speedle. Apache-2.0; see .github/codex/PAVU-LICENSE.txt.
"""Extract a ReviewBot response from a JSON body or event stream without logging response text.

Exit 10 permits one same-model retry. A stream that ends before its terminal event is
incomplete_stream: partial text is never published and the body is not a transport error.
"""

import json
from pathlib import Path
import re
import sys


OUTPUT_TOKEN_LIMIT = 10
STATUSES = {"completed", "incomplete", "failed", "in_progress", "queued", "cancelled"}
REASONS = {"max_output_tokens", "content_filter"}
ITEM_TYPES = {"message", "reasoning", "function_call", "web_search_call"}


def format_markdown(message):
    """Unwrap a whole ReviewBot report, preserving code examples inside it."""
    message = message.strip()
    wrapper = re.fullmatch(
        r"(?P<fence>`{3,}|~{3,})[ \t]*(?:text|markdown|md)?[ \t]*\r?\n"
        r"(?P<body>[\s\S]*?)\r?\n(?P=fence)[ \t]*",
        message, re.IGNORECASE,
    )
    if wrapper and re.match(r"## (?:Ark )?ReviewBot\b", wrapper["body"].lstrip()):
        message = wrapper["body"].strip()
    message = re.sub(r"\A## Ark ReviewBot(?=[ \t]|\r?$)",
                     "## ReviewBot", message, flags=re.MULTILINE)
    old_signature = "— **Ark ReviewBot**"
    if message.endswith(old_signature):
        message = message[:-len(old_signature)] + "— **ReviewBot**"
    return message


def label(value, allowed):
    if value is None:
        return "missing"
    return value if isinstance(value, str) and value in allowed else "other"


def diagnose(reason, response=None):
    # All string labels are allowlisted. Never log raw values, model text,
    # reasoning summaries, error messages, input, or JSON parser exceptions.
    response = response if isinstance(response, dict) else {}
    details = response.get("incomplete_details")
    details = details if isinstance(details, dict) else {}
    output = response.get("output")
    output = output if isinstance(output, list) else []
    usage = response.get("usage")
    usage = usage if isinstance(usage, dict) else {}
    tokens = {}
    for key in ("input_tokens", "output_tokens", "total_tokens"):
        value = usage.get(key)
        if type(value) is int and 0 <= value <= 10**12:
            tokens[key] = value
    output_details = usage.get("output_tokens_details")
    if isinstance(output_details, dict):
        value = output_details.get("reasoning_tokens")
        if type(value) is int and 0 <= value <= 10**12:
            tokens["reasoning_tokens"] = value
    diagnostic = {
        "reason_code": reason,
        "status": label(response.get("status"), STATUSES),
        "incomplete_reason": label(details.get("reason"), REASONS),
        "output_types": sorted({label(item.get("type"), ITEM_TYPES)
                                if isinstance(item, dict) else "other" for item in output}),
        "usage": tokens,
    }
    print("ReviewBot response: " + json.dumps(diagnostic, separators=(",", ":")), file=sys.stderr)


class IncompleteStream(Exception):
    """The event stream ended before its terminal event; carries a label-only snapshot."""

    def __init__(self, snapshot):
        super().__init__("incomplete_stream")
        self.snapshot = snapshot


def sse_lines(text):
    """Split on server-sent-event line endings only: CRLF, CR, or LF.

    JSON strings may contain U+2028, U+2029, U+0085 and the other separators
    that str.splitlines() breaks on. A terminal break does not add a line.
    """
    if not text:
        return []
    if text.endswith("\r\n"):
        body = text[:-2]
    elif text.endswith(("\n", "\r")):
        body = text[:-1]
    else:
        body = text
    if not body:
        return [""]
    return re.split(r"\r\n|\r|\n", body)


def sse_events(text):
    """Yield the data payload of each dispatched server-sent event.

    Follows the SSE framing rules: data lines accumulate until a blank line
    dispatches the event, comment lines start with ':', and an event that is
    not terminated by a blank line before the end of the body is discarded.
    """
    data = []
    for line in sse_lines(text):
        if not line:
            if data:
                yield "\n".join(data)
            data = []
            continue
        if line.startswith(":"):
            continue
        field, _, value = line.partition(":")
        if value.startswith(" "):
            value = value[1:]
        if field == "data":
            data.append(value)


def looks_like_sse(text):
    return any(re.match(r"(?:data|event|id|retry)?:", line)
               for line in sse_lines(text.lstrip())[:1])


def stream_payloads(text):
    for payload in sse_events(text):
        if payload == "[DONE]":
            yield None
            continue
        event = json.loads(payload)
        if not isinstance(event, dict):
            raise ValueError("event is not an object")
        yield event


def assemble_ark(text):
    """Return the response object of the first terminal Responses API event."""
    snapshot = {}
    for event in stream_payloads(text):
        if event is None:
            continue
        kind = event.get("type")
        response = event.get("response")
        if kind in ("response.completed", "response.incomplete", "response.failed"):
            if not isinstance(response, dict):
                raise ValueError("terminal event has no response object")
            return response
        if kind == "error":
            return {"status": "failed", "error": {"type": "stream_error"}}
        if isinstance(response, dict):
            snapshot = response
    raise IncompleteStream(snapshot)


def assemble_deepseek(text):
    """Join chat-completion chunks into the non-streaming response shape.

    Only delta.content is review text; reasoning_content is dropped. The stream
    counts as complete only after a chunk carries a non-null finish_reason;
    a [DONE] sentinel alone is not enough.
    """
    response = {"object": "chat.completion"}
    role = None
    parts = []
    finish_reason = None
    for event in stream_payloads(text):
        if event is None:
            continue
        if event.get("error"):
            return {"error": {"type": "stream_error"}}
        for key in ("id", "created", "model"):
            if key in event and key not in response:
                response[key] = event[key]
        if isinstance(event.get("usage"), dict):
            response["usage"] = event["usage"]
        if finish_reason is not None:
            continue
        choices = event.get("choices")
        if not isinstance(choices, list):
            continue
        for choice in choices:
            if not isinstance(choice, dict) or choice.get("index", 0) != 0:
                continue
            delta = choice.get("delta")
            if isinstance(delta, dict):
                if role is None and delta.get("role") is not None:
                    role = delta.get("role")
                content = delta.get("content")
                if content is not None:
                    if not isinstance(content, str):
                        raise ValueError("delta content is not a string")
                    parts.append(content)
            if choice.get("finish_reason") is not None:
                finish_reason = choice.get("finish_reason")
    message = {"role": role or "assistant", "content": "".join(parts) if parts else None}
    response["choices"] = [{"index": 0, "message": message, "finish_reason": finish_reason}]
    if finish_reason is None:
        raise IncompleteStream(response)
    return response


def load_response(text, provider):
    """Parse a plain JSON body, or reassemble a server-sent event stream."""
    try:
        return json.loads(text)
    except ValueError:
        if not looks_like_sse(text):
            raise
    if provider == "deepseek":
        return assemble_deepseek(text)
    return assemble_ark(text)


def extract(response_path, result_path, requested_model):
    try:
        response = load_response(Path(response_path).read_text(), "ark")
    except IncompleteStream as stream:
        # No terminal event: never publish partial text, and never treat it
        # as a transport error that the same request may be resent for.
        diagnose("incomplete_stream", stream.snapshot)
        return 1
    except (OSError, ValueError, RecursionError):
        diagnose("malformed_json")
        return 1
    if not isinstance(response, dict):
        diagnose("invalid_response")
        return 1

    status = response.get("status")
    details = response.get("incomplete_details")
    reason = details.get("reason") if isinstance(details, dict) else None
    if status == "incomplete" and reason == "max_output_tokens":
        diagnose("output_token_limit", response)
        return OUTPUT_TOKEN_LIMIT
    if status == "incomplete":
        diagnose("incomplete_response", response)
        return 11
    if status not in (None, "completed") or response.get("error"):
        diagnose("unsuccessful_response", response)
        return 1

    output = response.get("output")
    if not isinstance(output, list):
        diagnose("invalid_response", response)
        return 1
    has_reasoning = any(
        isinstance(item, dict) and item.get("type") == "reasoning" for item in output
    )
    parts = []
    for item in output:
        if not isinstance(item, dict) or item.get("type") != "message":
            continue
        if item.get("status") not in (None, "completed"):
            diagnose("incomplete_message", response)
            return 1
        content = item.get("content")
        if not isinstance(content, list):
            continue
        for part in content:
            if isinstance(part, dict) and part.get("type") == "output_text":
                text = part.get("text")
                if isinstance(text, str) and text.strip():
                    parts.append(text)
    message = format_markdown("\n".join(parts))
    if not message:
        if has_reasoning:
            diagnose("reasoning_only", response)
            return 12
        diagnose("empty_output", response)
        return 1

    model = response.get("model")
    if not isinstance(model, str) or not model.strip():
        model = requested_model
    Path(result_path).write_text(json.dumps({"message": message, "model": model}))
    return 0


if __name__ == "__main__":
    sys.exit(extract(*sys.argv[1:]))
