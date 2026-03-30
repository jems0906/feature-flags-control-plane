"""
Python SDK for the Feature Flag & Traffic Control Platform.

Install dependencies:
    pip install requests sseclient-py

Usage:
    from featureflags_sdk import FeatureFlagsClient

    client = FeatureFlagsClient(base_url="http://localhost:8080", env="production")

    # One-shot fetch
    flags = client.get_all_flags()

    # Background polling
    client.poll_flags(interval=10)

    # Real-time SSE hot-reload
    client.start_sse(on_update=lambda flags: print("Updated:", flags))

    # Evaluate a flag for a user
    enabled = client.is_enabled("dark-mode", user_id="alice", tenant_id="acme")

    # Get an A/B variant
    variant = client.get_variant("button-color", user_id="alice")

    # Check rate limit
    allowed = client.check_rate_limit(route="/api/search", user_id="alice")
"""

import json
import importlib
import threading
import time

import requests


class FeatureFlagsClient:
    def __init__(self, base_url: str, env: str = "production", auth_token: str = ""):
        self.base_url = base_url.rstrip("/")
        self.env = env
        self.auth_token = auth_token
        self.flags: list = []
        self._lock = threading.Lock()

    def _auth_headers(self) -> dict:
        if not self.auth_token:
            return {}
        return {"Authorization": f"Bearer {self.auth_token}"}

    # ------------------------------------------------------------------
    # Flag listing
    # ------------------------------------------------------------------

    def get_all_flags(self) -> list:
        """Fetch all flags for the configured environment."""
        resp = requests.get(f"{self.base_url}/flags/all", params={"env": self.env}, timeout=5)
        resp.raise_for_status()
        with self._lock:
            self.flags = resp.json()
        return self.flags

    def poll_flags(self, interval: int = 10) -> None:
        """Start a background thread that refreshes flags every `interval` seconds."""
        def _loop():
            while True:
                try:
                    self.get_all_flags()
                except Exception as exc:
                    print(f"[FeatureFlagsClient] poll error: {exc}")
                time.sleep(interval)

        t = threading.Thread(target=_loop, daemon=True)
        t.start()

    # ------------------------------------------------------------------
    # SSE hot-reload
    # ------------------------------------------------------------------

    def start_sse(self, on_update=None) -> None:
        """
        Open an SSE connection and call `on_update(flags)` whenever a flag
        change is pushed from the control plane. Reconnects automatically.
        Runs in a background daemon thread.
        """
        try:
            sseclient = importlib.import_module("sseclient")  # pip install sseclient-py
        except ImportError as e:
            raise ImportError("pip install sseclient-py") from e

        def _listen():
            while True:
                try:
                    url = f"{self.base_url}/flags/stream"
                    resp = requests.get(url, params={"env": self.env}, stream=True, timeout=None)
                    client = sseclient.SSEClient(resp)
                    for event in client.events():
                        if event.event == "update":
                            try:
                                data = json.loads(event.data)
                            except json.JSONDecodeError:
                                data = None
                            if not isinstance(data, list):
                                data = self.get_all_flags()
                            with self._lock:
                                self.flags = data
                            if on_update:
                                on_update(self.flags)
                        # "ping" events are keepalives — ignored
                except Exception as exc:
                    print(f"[FeatureFlagsClient] SSE error: {exc}; reconnecting in 5s")
                    time.sleep(5)

        t = threading.Thread(target=_listen, daemon=True)
        t.start()

    # ------------------------------------------------------------------
    # Flag evaluation
    # ------------------------------------------------------------------

    def is_enabled(self, flag_name: str, user_id: str = "", tenant_id: str = "",
                   headers: dict = None) -> bool:
        """Evaluate a feature flag for a specific user context."""
        payload = {"UserID": user_id, "TenantID": tenant_id, "Headers": headers or {}}
        resp = requests.post(
            f"{self.base_url}/flags/{flag_name}/evaluate",
            json=payload,
            timeout=5,
        )
        if not resp.ok:
            return False
        return resp.json().get("enabled", False)

    # ------------------------------------------------------------------
    # A/B experiments
    # ------------------------------------------------------------------

    def get_variant(self, experiment_name: str, user_id: str) -> str:
        """Get the deterministic variant assigned to a user for an experiment."""
        resp = requests.get(
            f"{self.base_url}/experiment/{experiment_name}/variant",
            params={"userId": user_id},
            timeout=5,
        )
        resp.raise_for_status()
        return resp.json().get("variant", "")

    def record_conversion(self, experiment_name: str, variant: str) -> None:
        """Record a conversion event for an experiment variant."""
        requests.post(
            f"{self.base_url}/experiment/{experiment_name}/convert",
            params={"variant": variant},
            headers=self._auth_headers(),
            timeout=5,
        )

    # ------------------------------------------------------------------
    # Rate limiting
    # ------------------------------------------------------------------

    def check_rate_limit(self, route: str, user_id: str = "", tenant_id: str = "") -> bool:
        """Returns True if the request is allowed, False if throttled. Fails open."""
        try:
            resp = requests.post(
                f"{self.base_url}/ratelimit/check",
                json={"route": route, "userId": user_id, "tenantId": tenant_id},
                timeout=2,
            )
            return resp.json().get("allowed", True)
        except Exception:
            return True  # fail open

    # ------------------------------------------------------------------
    # Circuit breaking
    # ------------------------------------------------------------------

    def get_circuit_breaker_state(self, route: str) -> str:
        """
        Returns the circuit breaker state for the given route.
        One of: "closed", "open", "half-open". Fails open (returns "closed")
        on any network or decode error.
        """
        try:
            resp = requests.get(
                f"{self.base_url}/circuitbreaker",
                params={"route": route},
                timeout=2,
            )
            return resp.json().get("state", "closed")
        except Exception:
            return "closed"  # fail open

    def check_circuit_breaker(self, route: str) -> tuple[bool, str]:
        """Returns (allowed, state) for the route. Fails open on errors."""
        try:
            resp = requests.post(
                f"{self.base_url}/circuitbreaker/check",
                json={"route": route},
                timeout=2,
            )
            data = resp.json()
            return data.get("allowed", True), data.get("state", "closed")
        except Exception:
            return True, "closed"

    def report_circuit_breaker(self, route: str, success: bool, latency_ms: int = 0) -> None:
        """
        Report a completed request outcome and latency to the control-plane
        so it can update the circuit breaker state for the given route.
        """
        try:
            requests.post(
                f"{self.base_url}/circuitbreaker/report",
                json={"route": route, "success": success, "latencyMs": latency_ms},
                headers=self._auth_headers(),
                timeout=2,
            )
        except Exception:
            pass  # non-critical reporting; swallow errors

