# Adapted from Pavu a7b458e; modified for Speedle. Apache-2.0; see .github/codex/PAVU-LICENSE.txt.
"""Ark endpoint, defaults, request payload and response extraction."""

from reviewbot_response import extract


URL = "https://ark.cn-beijing.volces.com/api/plan/v3/responses"
DEFAULT_MODELS = "kimi-k3.1-pro deepseek-v4.1-flash glm-5.3"
DEFAULT_MAX_OUTPUT_TOKENS = 32768
DEFAULT_REASONING_EFFORT = "medium"
DEFAULT_INPUT_LIMIT = 750000


def payload(model, config, inputs, attempt):
    return {
        "model": model,
        "input": inputs["combined"][attempt],
        "max_output_tokens": config["max_output_tokens"],
        "reasoning": {"effort": config["reasoning_effort"]},
    }
