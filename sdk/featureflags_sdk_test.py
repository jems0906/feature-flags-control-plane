import sys
import types
import unittest
from unittest.mock import patch

if "requests" not in sys.modules:
    fake_requests = types.SimpleNamespace(get=None, post=None)
    sys.modules["requests"] = fake_requests

from featureflags_sdk import FeatureFlagsClient


class FakeResponse:
    def __init__(self, payload=None, ok=True):
        self._payload = payload or {}
        self.ok = ok

    def json(self):
        return self._payload

    def raise_for_status(self):
        return None


class FeatureFlagsClientTests(unittest.TestCase):
    def test_record_conversion_uses_bearer_token(self):
        client = FeatureFlagsClient("http://localhost:8080", auth_token="secret-token")

        with patch("featureflags_sdk.requests.post", return_value=FakeResponse()) as post:
            client.record_conversion("button-color", "green")

        post.assert_called_once()
        _, kwargs = post.call_args
        self.assertEqual(kwargs["headers"], {"Authorization": "Bearer secret-token"})
        self.assertEqual(kwargs["params"], {"variant": "green"})

    def test_report_circuit_breaker_uses_bearer_token(self):
        client = FeatureFlagsClient("http://localhost:8080", auth_token="secret-token")

        with patch("featureflags_sdk.requests.post", return_value=FakeResponse()) as post:
            client.report_circuit_breaker("/demo/action", False, 320)

        post.assert_called_once()
        _, kwargs = post.call_args
        self.assertEqual(kwargs["headers"], {"Authorization": "Bearer secret-token"})
        self.assertEqual(
            kwargs["json"],
            {"route": "/demo/action", "success": False, "latencyMs": 320},
        )

    def test_check_circuit_breaker_returns_allowed_and_state(self):
        client = FeatureFlagsClient("http://localhost:8080")

        with patch(
            "featureflags_sdk.requests.post",
            return_value=FakeResponse({"allowed": False, "state": "half-open"}),
        ):
            allowed, state = client.check_circuit_breaker("/demo/action")

        self.assertFalse(allowed)
        self.assertEqual(state, "half-open")


if __name__ == "__main__":
    unittest.main()