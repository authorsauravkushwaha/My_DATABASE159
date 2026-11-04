"""Rate limiting, Prometheus metrics, and middleware-stack behavior."""
import uuid

from fastapi import FastAPI
from fastapi.testclient import TestClient
from test_api import register

from app.metrics import MetricsRegistry, normalize_path, reset_registry
from app.middleware import MetricsMiddleware, RateLimitMiddleware


def build_limited_app(**limits) -> FastAPI:
    limited = FastAPI()

    @limited.get("/ok")
    def ok():
        return {"ok": True}

    @limited.get("/v1/auth/login")
    def limited_login():
        return {"ok": True}

    limited.add_middleware(RateLimitMiddleware, **limits)
    return limited


def test_rate_limit_tiered_windows_and_headers():
    app = build_limited_app(limit=2, window=60, auth_limit=1, auth_window=60, disabled=False, trust_proxy=False)
    with TestClient(app) as client:
        first = client.get("/ok")
        assert first.status_code == 200
        assert first.headers["x-ratelimit-limit"] == "2"
        assert first.headers["x-ratelimit-remaining"] == "1"
        second = client.get("/ok")
        assert second.headers["x-ratelimit-remaining"] == "0"
        blocked = client.get("/ok")
        assert blocked.status_code == 429
        assert blocked.headers["retry-after"]
        assert blocked.headers["x-ratelimit-remaining"] == "0"
        assert blocked.json()["detail"] == "Rate limit exceeded. Retry later."
        # The auth tier has its own window and is exhausted separately.
        assert client.get("/v1/auth/login").status_code == 200
        assert client.get("/v1/auth/login").status_code == 429
        # Health-style probes of the outer app are exempt, and disabled mode sends no headers.
    disabled = build_limited_app(limit=1, window=60, disabled=True, trust_proxy=False)
    with TestClient(disabled) as client:
        for _ in range(5):
            response = client.get("/ok")
            assert response.status_code == 200
            assert "x-ratelimit-limit" not in response.headers


def test_rate_limit_window_resets_after_expiry(monkeypatch):
    from app import middleware as middleware_module
    from app.middleware import RateLimitMiddleware

    clock = {"now": 100.0}

    class FakeClock:
        @staticmethod
        def time():
            return clock["now"]

    monkeypatch.setattr(middleware_module, "time", FakeClock)
    middleware = RateLimitMiddleware(lambda *_: None, limit=1, window=60, auth_limit=1, auth_window=60, disabled=False, trust_proxy=False)
    allowed, remaining, retry_after = middleware._consume("global", "198.51.100.9", 1, 60)
    assert (allowed, remaining) == (True, 0)
    assert retry_after == 20  # 60s window that opened at t=100
    allowed, _, _ = middleware._consume("global", "198.51.100.9", 1, 60)
    assert allowed is False
    clock["now"] += 60  # move past the window boundary
    allowed, remaining, _ = middleware._consume("global", "198.51.100.9", 1, 60)
    assert (allowed, remaining) == (True, 0)


def test_rate_limit_honors_forwarded_client_when_trusted():
    app = build_limited_app(limit=1, window=60, auth_limit=1, auth_window=60, disabled=False, trust_proxy=True)
    with TestClient(app) as client:
        assert client.get("/ok", headers={"X-Forwarded-For": "203.0.113.7"}).status_code == 200
        assert client.get("/ok", headers={"X-Forwarded-For": "203.0.113.7"}).status_code == 429
        # A different forwarded client is unaffected.
        assert client.get("/ok", headers={"X-Forwarded-For": "203.0.113.8"}).status_code == 200


def test_real_app_rate_limit_headers_and_health_exemption(client):
    probed = client.get("/metrics")
    assert probed.status_code == 200
    assert probed.headers["x-ratelimit-limit"] == "1000000"
    health = client.get("/health")
    assert health.status_code == 200
    assert "x-ratelimit-limit" not in health.headers


def test_metrics_endpoint_renders_prometheus_families(client):
    register(client, "metrics@example.com")
    body = client.get("/metrics")
    assert body.status_code == 200
    assert body.headers["content-type"].startswith("text/plain")
    text = body.text
    assert "# TYPE kestrel_http_requests_total counter" in text
    assert "# TYPE kestrel_http_request_duration_seconds histogram" in text
    assert 'kestrel_build_info{version="0.3.0"} 1' in text
    assert 'kestrel_http_requests_total{method="POST",status="201",path="/v1/auth/register"}' in text
    assert 'kestrel_http_request_duration_seconds_bucket{path="/metrics",le="+Inf"}' in text


def test_metrics_endpoint_optional_token_gate(client, monkeypatch):
    monkeypatch.setenv("METRICS_TOKEN", "metrics-secret-value-at-least-32-chars")
    unauthorized = client.get("/metrics")
    assert unauthorized.status_code == 401
    allowed = client.get("/metrics", headers={"Authorization": "Bearer metrics-secret-value-at-least-32-chars"})
    assert allowed.status_code == 200
    # An unrelated bearer credential is rejected like any bad token.
    wrong = client.get("/metrics", headers={"Authorization": "Bearer something-else-entirely"})
    assert wrong.status_code == 401


def test_metrics_path_normalization_bounds_cardinality():
    project_id = str(uuid.uuid4())
    assert normalize_path(f"/v1/projects/{project_id}/data/notes/batch") == "/v1/projects/{id}/data/{name}/batch"
    assert normalize_path(f"/v1/organizations/{project_id}/projects") == "/v1/organizations/{id}/projects"
    assert normalize_path(f"/v1/projects/{project_id}/collections") == "/v1/projects/{id}/collections"
    assert normalize_path("/health") == "/health"

    registry = MetricsRegistry(max_path_patterns=3, version="test")
    # Distinct pre-normalization paths collapse, and overflow patterns fold into "other".
    registry.observe("GET", 200, f"/v1/projects/{project_id}/data/coll0", 0.001)
    registry.observe("GET", 200, f"/v1/projects/{project_id}/data/coll1", 0.001)
    registry.observe("GET", 200, "/health", 0.001)
    registry.observe("GET", 200, f"/v1/organizations/{project_id}/projects", 0.001)  # 4th pattern -> other
    registry.observe("GET", 200, "/another/overflow", 0.001)  # -> other
    rendered = registry.render()
    # Three bounded patterns plus the overflow bucket.
    assert rendered.count("kestrel_http_requests_total{") == 4
    assert 'path="other"' in rendered
    assert 'path="/v1/projects/{id}/data/{name}"' in rendered


def test_metrics_histogram_buckets_are_cumulative():
    registry = MetricsRegistry(version="test")
    registry.observe("GET", 200, "/fast", 0.001)
    registry.observe("GET", 200, "/slow", 1.5)
    rendered = registry.render()
    fast_le = [line for line in rendered.splitlines() if line.startswith('kestrel_http_request_duration_seconds_bucket{path="/fast"')]
    values = {line.split('le="')[1].split('"')[0]: int(line.rsplit(" ", 1)[1]) for line in fast_le}
    assert values["0.005"] == 1 and values["+Inf"] == 1
    slow_count = [line for line in rendered.splitlines() if line.startswith('kestrel_http_request_duration_seconds_count{path="/slow"')][0]
    assert slow_count.endswith(" 1")
    slow_sum = [line for line in rendered.splitlines() if line.startswith('kestrel_http_request_duration_seconds_sum{path="/slow"')][0]
    assert 1.5 <= float(slow_sum.rsplit(" ", 1)[1]) <= 1.6


def test_metrics_reset_starts_clean():
    registry = reset_registry(version="0.3.0")
    registry.observe("GET", 200, "/health", 0.001)
    fresh = reset_registry(version="0.3.0")
    assert fresh is not registry
    assert "kestrel_http_requests_total{" not in fresh.render()


def test_security_headers_apply_to_body_limit_rejections(client):
    """Regression: security headers must wrap the body limiter, not sit inside it."""
    response = client.post("/v1/auth/login", content=b"x" * 10, headers={"Content-Length": "131073"})
    assert response.status_code == 413
    assert response.headers["x-content-type-options"] == "nosniff"
    assert response.headers["cache-control"] == "no-store"
    assert response.headers["x-frame-options"] == "DENY"
    # Metrics still observe rejected requests.
    metrics_text = client.get("/metrics").text
    assert 'kestrel_http_requests_total{method="POST",status="413",path="/v1/auth/login"}' in metrics_text


def test_metrics_middleware_counts_exceptions_without_swallowing():
    observing = FastAPI()

    @observing.get("/boom")
    def boom():
        raise RuntimeError("intentional failure")

    registry = MetricsRegistry(version="test")
    observing.add_middleware(MetricsMiddleware, registry=registry)
    with TestClient(observing, raise_server_exceptions=False) as client:
        assert client.get("/boom").status_code == 500
    rendered = registry.render()
    assert 'kestrel_http_requests_total{method="GET",status="500",path="/boom"} 1' in rendered


def test_env_rate_limit_defaults_documented_and_parseable(monkeypatch):
    from app.middleware import RateLimitMiddleware

    async def passthrough(scope, receive, send):
        await send({"type": "http.response.start", "status": 200, "headers": []})

    for variable in (
        "RATE_LIMIT_MAX_REQUESTS",
        "RATE_LIMIT_WINDOW_SECONDS",
        "RATE_LIMIT_AUTH_MAX_REQUESTS",
        "RATE_LIMIT_AUTH_WINDOW_SECONDS",
        "RATE_LIMIT_DISABLED",
        "RATE_LIMIT_TRUST_PROXY",
    ):
        monkeypatch.delenv(variable, raising=False)
    middleware = RateLimitMiddleware(passthrough)
    assert middleware.limit == 1200 and middleware.window == 60
    assert middleware.auth_limit == 20 and middleware.auth_window == 60
    assert middleware.disabled is False and middleware.trust_proxy is False
