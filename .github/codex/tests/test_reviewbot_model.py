"""Local tests for ReviewBot provider selection, response validation and failover."""

import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import reviewbot_ark
import reviewbot_deepseek
import reviewbot_handoff
import reviewbot_model


def ark_response(text=None, output=None, status="completed", incomplete_reason=None, model=None):
    if output is None:
        output = [{
            "type": "message",
            "status": "completed",
            "content": [{"type": "output_text", "text": text}],
        }]
    result = {"status": status, "output": output}
    if incomplete_reason:
        result["incomplete_details"] = {"reason": incomplete_reason}
    if model:
        result["model"] = model
    return result


def deepseek_response(content=None, finish_reason="stop", reasoning=None,
                      model="deepseek-flash", usage=None):
    message = {"role": "assistant", "content": content}
    if reasoning is not None:
        message["reasoning_content"] = reasoning
    result = {
        "id": "chatcmpl-test",
        "object": "chat.completion",
        "choices": [{"index": 0, "message": message, "finish_reason": finish_reason}],
    }
    if model:
        result["model"] = model
    if usage:
        result["usage"] = usage
    return result


class ReviewBotTestCase(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        root = Path(self.temporary.name)
        self.input = root / "input.txt"
        self.retry_input = root / "retry-input.txt"
        self.system = root / "system.txt"
        self.user = root / "user.txt"
        self.retry_user = root / "retry-user.txt"
        self.result = root / "result.json"
        self.request = root / "request.json"
        self.response = root / "response.json"
        self.input.write_text("full input", encoding="utf-8")
        self.retry_input.write_text("reduced input", encoding="utf-8")
        self.system.write_text("system prompt", encoding="utf-8")
        self.user.write_text("full user input", encoding="utf-8")
        self.retry_user.write_text("reduced user input", encoding="utf-8")
        self.config = reviewbot_model.configuration({"ARK_API_KEY": "test-only"})
        self.calls = []
        self.scenarios = []

    def tearDown(self):
        self.temporary.cleanup()

    def transport(self, request_path, response_path, _timeout):
        payload = json.loads(Path(request_path).read_text(encoding="utf-8"))
        self.calls.append(payload)
        scenario = self.scenarios.pop(0)
        if isinstance(scenario, Exception):
            raise scenario
        status, body = scenario
        if isinstance(body, str):
            Path(response_path).write_text(body, encoding="utf-8")
        else:
            Path(response_path).write_text(json.dumps(body), encoding="utf-8")
        return status

    def run_models(self):
        errors = io.StringIO()
        output = io.StringIO()
        paths = {
            "input": self.input,
            "retry_input": self.retry_input,
            "system": self.system,
            "user": self.user,
            "retry_user": self.retry_user,
            "result": self.result,
            "request": self.request,
            "response": self.response,
        }
        with contextlib.redirect_stderr(errors), contextlib.redirect_stdout(output):
            retried = reviewbot_model.run(
                self.config, paths, transport=self.transport, clock=lambda: 0,
            )
        return retried, errors.getvalue() + output.getvalue()


class ProviderSelectionTests(unittest.TestCase):
    def test_ark_is_the_default(self):
        config = reviewbot_model.configuration({"ARK_API_KEY": "test-only"})
        self.assertEqual(config["provider"], "ark")
        self.assertEqual(config["models"], reviewbot_ark.DEFAULT_MODELS.split())

    def test_empty_provider_selects_ark(self):
        config = reviewbot_model.configuration({
            "REVIEWBOT_PROVIDER": "", "ARK_API_KEY": "test-only",
        })
        self.assertEqual(config["provider"], "ark")

    def test_unknown_provider_is_rejected(self):
        with self.assertRaises(reviewbot_model.ConfigurationError):
            reviewbot_model.configuration({
                "REVIEWBOT_PROVIDER": "other", "ARK_API_KEY": "test-only",
            })

    def test_deepseek_requires_its_model_identifier_to_be_safe(self):
        with self.assertRaises(reviewbot_model.ConfigurationError):
            reviewbot_model.configuration({
                "REVIEWBOT_PROVIDER": "deepseek",
                "DEEPSEEK_API_KEY": "test-only",
                "DEEPSEEK_REVIEW_MODEL": "bad model\nid",
            })

    def test_main_requires_the_selected_provider_key(self):
        errors = io.StringIO()
        with contextlib.redirect_stderr(errors):
            status = reviewbot_model.main(
                ["in", "retry", "result", "request", "response", "system", "user", "retry-user"],
                {"REVIEWBOT_PROVIDER": "deepseek", "DEEPSEEK_API_KEY": ""},
            )
        self.assertEqual(status, 1)
        self.assertIn("reason_code=invalid_configuration", errors.getvalue())


class ArkFailoverTests(ReviewBotTestCase):
    def test_http_error_fails_over_to_first_good_model(self):
        self.scenarios = [
            (500, {}),
            (200, ark_response("## ReviewBot\n\nSecond model succeeded.")),
        ]
        retried, errors = self.run_models()
        self.assertFalse(retried)
        self.assertEqual([call["model"] for call in self.calls], [
            "kimi-k3.1-pro", "deepseek-v4.1-flash",
        ])
        self.assertEqual(json.loads(self.result.read_text())["model"], "deepseek-v4.1-flash")
        self.assertIn("http_error", errors)
        self.assertNotIn("all_models_failed", errors)
        self.assertEqual(self.calls[0]["max_output_tokens"], 32768)
        self.assertEqual(self.calls[0]["reasoning"], {"effort": "medium"})

    def test_reasoning_only_response_is_rejected_and_fails_over(self):
        self.scenarios = [
            (200, ark_response(output=[{"type": "reasoning", "summary": []}])),
            (200, ark_response("## ReviewBot\n\nUsable review.", model="deepseek-v4.1-flash")),
        ]
        _, errors = self.run_models()
        self.assertEqual(len(self.calls), 2)
        self.assertIn("reasoning_only", errors)
        self.assertEqual(json.loads(self.result.read_text())["model"], "deepseek-v4.1-flash")

    def test_timeout_and_incomplete_response_fail_over(self):
        self.scenarios = [
            reviewbot_model.ModelFailure("request_timeout"),
            (200, ark_response(output=[], status="incomplete", incomplete_reason="content_filter")),
            (200, ark_response("## ReviewBot\n\nThird model succeeded.")),
        ]
        _, errors = self.run_models()
        self.assertEqual(
            [call["model"] for call in self.calls],
            reviewbot_ark.DEFAULT_MODELS.split(),
        )
        self.assertIn("request_timeout", errors)
        self.assertIn("incomplete_response", errors)
        self.assertTrue(self.result.is_file())

    def test_output_limit_retries_same_model_with_reduced_input(self):
        self.scenarios = [
            (200, ark_response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
            (200, ark_response("## ReviewBot\n\nRetry succeeded.")),
        ]
        retried, _ = self.run_models()
        self.assertTrue(retried)
        self.assertEqual([call["model"] for call in self.calls], ["kimi-k3.1-pro"] * 2)
        self.assertEqual([call["input"] for call in self.calls], ["full input", "reduced input"])

    def test_output_limit_retry_exhaustion_logs_one_reason_code(self):
        self.config["models"] = ["kimi-k3.1-pro"]
        self.scenarios = [
            (200, ark_response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
            (200, ark_response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
        ]
        retried, errors = self.run_models()
        self.assertIsNone(retried)
        model_errors = [
            line for line in errors.splitlines()
            if line.startswith("ReviewBot reason_code=") and " model=" in line
        ]
        self.assertEqual(model_errors, [
            "ReviewBot reason_code=retry_exhausted model=kimi-k3.1-pro",
        ])
        self.assertNotIn("reason_code=unusable_output", errors)

    def test_next_model_starts_with_full_input_and_fresh_retry_state(self):
        self.scenarios = [
            (200, ark_response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
            (200, ark_response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
            (200, ark_response("## ReviewBot\n\nNext model succeeded.")),
        ]
        retried, _ = self.run_models()
        self.assertFalse(retried)
        self.assertEqual([call["model"] for call in self.calls], [
            "kimi-k3.1-pro", "kimi-k3.1-pro", "deepseek-v4.1-flash",
        ])
        self.assertEqual([call["input"] for call in self.calls], [
            "full input", "reduced input", "full input",
        ])

    def test_all_models_failed_publishes_nothing(self):
        self.scenarios = [(503, {})] * 3
        self.result.write_text("stale result", encoding="utf-8")
        retried, errors = self.run_models()
        self.assertIsNone(retried)
        self.assertEqual(len(self.calls), 3)
        self.assertFalse(self.result.exists())
        self.assertIn("reason_code=all_models_failed", errors)

    def test_curl_reads_authorization_header_from_stdin(self):
        self.request.write_text("{}", encoding="utf-8")
        self.response.write_text("{}", encoding="utf-8")
        completed = subprocess.CompletedProcess(args=[], returncode=0, stdout="200")
        with mock.patch.dict(os.environ, {"REVIEWBOT_API_KEY": "test-secret"}):
            with mock.patch.object(reviewbot_model.subprocess, "run", return_value=completed) as run:
                status = reviewbot_model.http_post(
                    self.request, self.response, 30, "https://provider.example/v1/chat"
                )
        self.assertEqual(status, 200)
        arguments = run.call_args.args[0]
        self.assertNotIn("test-secret", "\0".join(arguments))
        self.assertIn("https://provider.example/v1/chat", arguments)
        self.assertEqual(arguments[arguments.index("--config") + 1], "-")
        self.assertEqual(
            run.call_args.kwargs["input"],
            'header = "Authorization: Bearer test-secret"\n',
        )

    def test_tunables_default_in_provider_modules(self):
        self.assertEqual(self.config["provider"], "ark")
        self.assertEqual(self.config["models"], reviewbot_ark.DEFAULT_MODELS.split())
        self.assertEqual(self.config["max_output_tokens"], 32768)
        self.assertEqual(self.config["reasoning_effort"], "medium")
        self.assertEqual(self.config["input_limit"], 750000)


class DeepSeekTests(ReviewBotTestCase):
    def setUp(self):
        super().setUp()
        self.config = reviewbot_model.configuration({
            "REVIEWBOT_PROVIDER": "deepseek",
            "DEEPSEEK_API_KEY": "test-only",
        })

    def test_normal_review_uses_chat_messages_and_ignores_reasoning(self):
        self.scenarios = [(
            200,
            deepseek_response(
                "## ReviewBot review\n\nNo actionable findings.",
                reasoning="internal reasoning that must not be published",
                usage={
                    "prompt_tokens": 10,
                    "completion_tokens": 20,
                    "total_tokens": 30,
                    "completion_tokens_details": {"reasoning_tokens": 5},
                },
            ),
        )]
        retried, errors = self.run_models()
        self.assertFalse(retried)
        self.assertEqual(len(self.calls), 1)
        payload = self.calls[0]
        self.assertEqual(payload["model"], "deepseek-flash")
        self.assertEqual(payload["max_tokens"], 65536)
        self.assertFalse(payload["stream"])
        self.assertEqual(payload["messages"], [
            {"role": "system", "content": "system prompt"},
            {"role": "user", "content": "full user input"},
        ])
        self.assertNotIn("test-only", json.dumps(payload))
        result = json.loads(self.result.read_text())
        self.assertEqual(result["message"], "## ReviewBot review\n\nNo actionable findings.")
        self.assertEqual(result["model"], "deepseek-flash")
        self.assertNotIn("internal reasoning", result["message"])
        self.assertNotIn("internal reasoning", errors)

    def test_finish_reason_length_retries_with_reduced_user_input(self):
        self.scenarios = [
            (200, deepseek_response(None, finish_reason="length")),
            (200, deepseek_response("## ReviewBot review\n\nRetried.")),
        ]
        retried, errors = self.run_models()
        self.assertTrue(retried)
        self.assertEqual([call["model"] for call in self.calls], ["deepseek-flash"] * 2)
        self.assertEqual(
            [call["messages"][1]["content"] for call in self.calls],
            ["full user input", "reduced user input"],
        )
        self.assertIn("output_token_limit", errors)
        self.assertIn("retrying once", errors)
        self.assertEqual(
            json.loads(self.result.read_text())["message"],
            "## ReviewBot review\n\nRetried.",
        )

    def test_finish_reason_length_retry_exhaustion_publishes_nothing(self):
        self.scenarios = [
            (200, deepseek_response(None, finish_reason="length")),
            (200, deepseek_response(None, finish_reason="length")),
        ]
        retried, errors = self.run_models()
        self.assertIsNone(retried)
        self.assertIn("reason_code=retry_exhausted model=deepseek-flash", errors)
        self.assertIn("reason_code=all_models_failed", errors)
        self.assertFalse(self.result.exists())

    def test_http_error_logs_status_only_and_publishes_nothing(self):
        self.scenarios = [(500, {"error": {"message": "secret provider detail"}})]
        retried, errors = self.run_models()
        self.assertIsNone(retried)
        self.assertIn("http_error HTTP 500 model=deepseek-flash", errors)
        self.assertIn("response body omitted", errors)
        self.assertNotIn("secret provider detail", errors)
        self.assertFalse(self.result.exists())

    def test_malformed_json_is_unusable(self):
        self.scenarios = [(200, "{not json")]
        retried, errors = self.run_models()
        self.assertIsNone(retried)
        self.assertIn("malformed_json", errors)
        self.assertIn("reason_code=all_models_failed", errors)
        self.assertFalse(self.result.exists())

    def test_content_filter_is_incomplete(self):
        self.scenarios = [(200, deepseek_response("partial", finish_reason="content_filter"))]
        retried, errors = self.run_models()
        self.assertIsNone(retried)
        self.assertIn("incomplete_response", errors)
        self.assertFalse(self.result.exists())

    def test_reasoning_only_response_is_rejected(self):
        self.scenarios = [(200, deepseek_response(None, reasoning="only reasoning"))]
        retried, errors = self.run_models()
        self.assertIsNone(retried)
        self.assertIn("reasoning_only", errors)
        self.assertFalse(self.result.exists())

    def test_tunables_default_in_provider_modules(self):
        self.assertEqual(self.config["provider"], "deepseek")
        self.assertEqual(self.config["models"], [reviewbot_deepseek.DEFAULT_MODEL])
        self.assertEqual(self.config["max_output_tokens"], reviewbot_deepseek.DEFAULT_MAX_OUTPUT_TOKENS)
        self.assertEqual(self.config["input_limit"], reviewbot_deepseek.DEFAULT_INPUT_LIMIT)

    def test_model_override(self):
        config = reviewbot_model.configuration({
            "REVIEWBOT_PROVIDER": "deepseek",
            "DEEPSEEK_API_KEY": "test-only",
            "DEEPSEEK_REVIEW_MODEL": "deepseek-v4-pro",
        })
        self.assertEqual(config["models"], ["deepseek-v4-pro"])


class InputLimitTests(unittest.TestCase):
    def test_deepseek_system_and_user_share_one_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            system = Path(directory) / "system.txt"
            user = Path(directory) / "user.txt"
            system.write_text("system", encoding="utf-8")
            user.write_text("0123456789", encoding="utf-8")
            system_text, user_text = reviewbot_model.limited_messages(system, user, 10)
        self.assertEqual(system_text, "system")
        self.assertTrue(user_text.startswith("0123"))
        self.assertIn("[Review input truncated at 10 bytes.", user_text)


class HandoffTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        self.result = root / "result.json"
        self.artifact = root / "handoff" / "report.json"
        self.result.write_text(json.dumps({
            "message": "## ReviewBot review\n\nNo findings.",
            "model": "deepseek-flash",
        }), encoding="utf-8")

    def seal(self, provider, secrets):
        environment = {
            "REVIEWBOT_SECRETS": json.dumps(secrets),
            "REVIEWBOT_PROVIDER": provider,
            "REVIEWBOT_RETRIED": "false",
            "GH_TOKEN": "github-token",
            "GITHUB_RUN_ID": "900",
            "GITHUB_RUN_ATTEMPT": "1",
            "PULL_NUMBER": "18",
            "HEAD_SHA": "a" * 40,
            "COMMAND": "review",
        }
        errors = io.StringIO()
        with mock.patch.dict(os.environ, environment, clear=False):
            with contextlib.redirect_stderr(errors):
                status = reviewbot_handoff.seal(self.result, self.artifact)
        return status, errors.getvalue()

    def test_deepseek_handoff_accepts_the_deepseek_key(self):
        status, errors = self.seal("deepseek", {"DEEPSEEK_API_KEY": "deepseek-secret"})
        self.assertEqual(status, 0, errors)
        self.assertTrue(self.artifact.is_file())

    def test_deepseek_handoff_requires_the_deepseek_key(self):
        status, errors = self.seal("deepseek", {"ARK_API_KEY": "ark-secret"})
        self.assertEqual(status, 1)
        self.assertIn("invalid_handoff", errors)
        self.assertFalse(self.artifact.exists())

    def test_ark_handoff_is_unchanged(self):
        status, errors = self.seal("", {"ARK_API_KEY": "ark-secret"})
        self.assertEqual(status, 0, errors)

    def test_unknown_provider_is_rejected(self):
        status, _ = self.seal("other", {"ARK_API_KEY": "ark-secret"})
        self.assertEqual(status, 1)


if __name__ == "__main__":
    unittest.main()
