"""Local tests for ReviewBot response validation and ordered model failover."""

import contextlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import reviewbot_ark


def response(text=None, output=None, status="completed", incomplete_reason=None, model=None):
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


class ArkFailoverTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        root = Path(self.temporary.name)
        self.input = root / "input.txt"
        self.retry_input = root / "retry-input.txt"
        self.result = root / "result.json"
        self.request = root / "request.json"
        self.response = root / "response.json"
        self.input.write_text("full input", encoding="utf-8")
        self.retry_input.write_text("reduced input", encoding="utf-8")
        self.config = reviewbot_ark.configuration({"ARK_API_KEY": "test-only"})
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
        Path(response_path).write_text(json.dumps(body), encoding="utf-8")
        return status

    def run_models(self):
        errors = io.StringIO()
        with contextlib.redirect_stderr(errors):
            retried = reviewbot_ark.run(
                self.config,
                self.input,
                self.retry_input,
                self.result,
                self.request,
                self.response,
                transport=self.transport,
                clock=lambda: 0,
            )
        return retried, errors.getvalue()

    def test_http_error_fails_over_to_first_good_model(self):
        self.scenarios = [
            (500, {}),
            (200, response("## ReviewBot\n\nSecond model succeeded.")),
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
            (200, response(output=[{"type": "reasoning", "summary": []}])),
            (200, response("## ReviewBot\n\nUsable review.", model="deepseek-v4.1-flash")),
        ]
        _, errors = self.run_models()
        self.assertEqual(len(self.calls), 2)
        self.assertIn("reasoning_only", errors)
        self.assertEqual(json.loads(self.result.read_text())["model"], "deepseek-v4.1-flash")

    def test_timeout_and_incomplete_response_fail_over(self):
        self.scenarios = [
            reviewbot_ark.ModelFailure("request_timeout"),
            (200, response(output=[], status="incomplete", incomplete_reason="content_filter")),
            (200, response("## ReviewBot\n\nThird model succeeded.")),
        ]
        _, errors = self.run_models()
        self.assertEqual([call["model"] for call in self.calls], reviewbot_ark.DEFAULT_MODELS.split())
        self.assertIn("request_timeout", errors)
        self.assertIn("incomplete_response", errors)
        self.assertTrue(self.result.is_file())

    def test_output_limit_retries_same_model_with_reduced_input(self):
        self.scenarios = [
            (200, response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
            (200, response("## ReviewBot\n\nRetry succeeded.")),
        ]
        retried, _ = self.run_models()
        self.assertTrue(retried)
        self.assertEqual([call["model"] for call in self.calls], ["kimi-k3.1-pro"] * 2)
        self.assertEqual([call["input"] for call in self.calls], ["full input", "reduced input"])

    def test_next_model_starts_with_full_input_and_fresh_retry_state(self):
        self.scenarios = [
            (200, response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
            (200, response(output=[], status="incomplete", incomplete_reason="max_output_tokens")),
            (200, response("## ReviewBot\n\nNext model succeeded.")),
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

    def test_tunables_default_in_script(self):
        self.assertEqual(self.config["models"], reviewbot_ark.DEFAULT_MODELS.split())
        self.assertEqual(self.config["max_output_tokens"], 32768)
        self.assertEqual(self.config["reasoning_effort"], "medium")
        self.assertEqual(self.config["input_limit"], 750000)


if __name__ == "__main__":
    unittest.main()
