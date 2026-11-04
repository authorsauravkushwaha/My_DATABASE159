"""ASGI middleware: body-size bounds, fixed-window rate limiting, and HTTP metrics.

Control-plane auth/admin routes stay strictly capped at 128 KiB, single-record
and query routes allow up to 1 MiB, and bulk batch/NDJSON dataset ingestion
routes allow up to 16 MiB.

Rate limiting is a per-client fixed window in process memory: generous defaults
protect against accidental floods and credential stuffing without pretending to
replace a distributed edge limiter (see docs/deployment.md).
"""
import os
import threading
import time

from starlette.responses import JSONResponse

DEFAULT_MAX_BYTES = 131_072  # 128 KiB
DATA_ROUTE_MAX_BYTES = 1_048_576  # 1 MiB
BULK_ROUTE_MAX_BYTES = int(os.getenv("MAX_BULK_BODY_BYTES", str(16 * 1024 * 1024)))  # 16 MiB

RATE_LIMIT_EXEMPT_PATHS = {"/health", "/ready"}
AUTH_RATE_LIMIT_PREFIX = "/v1/auth/"
MAX_RATE_TRACKED_CLIENTS = 10_000


def _env_flag(name: str, default: str = "false") -> bool:
    return os.getenv(name, default).strip().lower() in {"1", "true", "yes", "on"}


def _route_limit(path: str, default_limit: int) -> tuple[int, str]:
    if path.startswith("/v1/projects/") and "/data/" in path:
        if path.endswith("/batch") or path.endswith("/import"):
            return BULK_ROUTE_MAX_BYTES, "16 MiB"
        return DATA_ROUTE_MAX_BYTES, "1 MiB"
    return default_limit, "128 KiB"


class RequestBodyLimitMiddleware:
    def __init__(self, app, max_bytes: int = DEFAULT_MAX_BYTES):
        self.app = app
        self.max_bytes = max_bytes

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        limit, label = _route_limit(scope.get("path", ""), self.max_bytes)
        headers = dict(scope.get("headers", []))
        try:
            declared = int(headers.get(b"content-length", b"0"))
        except ValueError:
            await JSONResponse({"detail": "Invalid Content-Length"}, status_code=400)(scope, receive, send)
            return
        too_large = JSONResponse({"detail": f"Request body exceeds the {label} limit"}, status_code=413)
        if declared > limit:
            await too_large(scope, receive, send)
            return

        # Buffer at most the route limit, then replay the request to the application.
        messages, received = [], 0
        while True:
            message = await receive()
            messages.append(message)
            if message["type"] != "http.request":
                break
            received += len(message.get("body", b""))
            if received > limit:
                await too_large(scope, receive, send)
                return
            if not message.get("more_body", False):
                break
        pending = iter(messages)

        async def replay_receive():
            try:
                return next(pending)
            except StopIteration:
                return await receive()

        await self.app(scope, replay_receive, send)


class RateLimitMiddleware:
    """Fixed-window per-client request limits with ``X-RateLimit-*`` response headers.

    Two tiers: a strict per-IP window on ``/v1/auth/*`` (credential-stuffing
    resistance) and a generous global window on everything else. Configuration:

    - ``RATE_LIMIT_MAX_REQUESTS`` / ``RATE_LIMIT_WINDOW_SECONDS`` (default 1200 / 60)
    - ``RATE_LIMIT_AUTH_MAX_REQUESTS`` / ``RATE_LIMIT_AUTH_WINDOW_SECONDS`` (default 20 / 60)
    - ``RATE_LIMIT_DISABLED=true`` turns enforcement (and headers) off
    - ``RATE_LIMIT_TRUST_PROXY=true`` keys on the first ``X-Forwarded-For`` hop
      instead of the direct peer address; only enable it behind a proxy that
      overwrites that header.
    """

    def __init__(self, app, limit: int | None = None, window: int | None = None,
                 auth_limit: int | None = None, auth_window: int | None = None,
                 disabled: bool | None = None, trust_proxy: bool | None = None):
        self.app = app
        self.limit = int(os.getenv("RATE_LIMIT_MAX_REQUESTS", "1200")) if limit is None else limit
        self.window = int(os.getenv("RATE_LIMIT_WINDOW_SECONDS", "60")) if window is None else window
        self.auth_limit = int(os.getenv("RATE_LIMIT_AUTH_MAX_REQUESTS", "20")) if auth_limit is None else auth_limit
        self.auth_window = int(os.getenv("RATE_LIMIT_AUTH_WINDOW_SECONDS", "60")) if auth_window is None else auth_window
        self.disabled = _env_flag("RATE_LIMIT_DISABLED") if disabled is None else disabled
        self.trust_proxy = _env_flag("RATE_LIMIT_TRUST_PROXY") if trust_proxy is None else trust_proxy
        self._lock = threading.Lock()
        self._buckets: dict[tuple[str, str], tuple[int, int]] = {}

    def _client_key(self, scope) -> str:
        if self.trust_proxy:
            forwarded = None
            for name, value in scope.get("headers", []):
                if name == b"x-forwarded-for":
                    forwarded = value.decode("latin-1")
                    break
            if forwarded:
                return forwarded.split(",")[0].strip()
        client = scope.get("client")
        return client[0] if client else "unknown"

    def _consume(self, bucket: str, identity: str, limit: int, window: int) -> tuple[bool, int, int]:
        """Return (allowed, remaining, seconds_until_reset)."""
        if limit <= 0:
            return True, 0, 0
        now = int(time.time())
        slot = now - (now % window)
        with self._lock:
            key = (bucket, identity)
            window_start, count = self._buckets.get(key, (slot, 0))
            if window_start != slot:
                window_start, count = slot, 0
            if len(self._buckets) >= MAX_RATE_TRACKED_CLIENTS and key not in self._buckets:
                # Bound memory: drop expired windows before admitting a new client.
                self._buckets = {k: v for k, v in self._buckets.items() if v[0] == window_start}
            count += 1
            self._buckets[key] = (window_start, count)
            allowed = count <= limit
            retry_after = max(1, window - (now - window_start))
            return allowed, max(0, limit - count), retry_after

    async def __call__(self, scope, receive, send):
        if self.disabled or scope["type"] != "http":
            return await self.app(scope, receive, send)
        path = scope.get("path", "")
        if path in RATE_LIMIT_EXEMPT_PATHS:
            return await self.app(scope, receive, send)

        bucket = "auth" if path.startswith(AUTH_RATE_LIMIT_PREFIX) else "global"
        limit = self.auth_limit if bucket == "auth" else self.limit
        window = self.auth_window if bucket == "auth" else self.window
        allowed, remaining, retry_after = self._consume(bucket, self._client_key(scope), limit, window)

        if not allowed:
            async def send_limited(message):
                if message["type"] == "http.response.start":
                    headers = list(message.get("headers", []))
                    headers.extend([
                        (b"x-ratelimit-limit", str(limit).encode()),
                        (b"x-ratelimit-remaining", b"0"),
                    ])
                    message = {**message, "headers": headers}
                await send(message)

            await JSONResponse(
                {"detail": "Rate limit exceeded. Retry later."},
                status_code=429,
                headers={"Retry-After": str(max(1, retry_after))},
            )(scope, receive, send_limited)
            return

        async def send_with_limit(message):
            if message["type"] == "http.response.start":
                headers = list(message.get("headers", []))
                headers.extend([
                    (b"x-ratelimit-limit", str(limit).encode()),
                    (b"x-ratelimit-remaining", str(remaining).encode()),
                ])
                message = {**message, "headers": headers}
            await send(message)

        await self.app(scope, receive, send_with_limit)


class MetricsMiddleware:
    """Record request count and latency for every HTTP response into the metrics registry."""

    def __init__(self, app, registry=None):
        self.app = app
        self._registry = registry

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        from app.metrics import get_registry

        registry = self._registry or get_registry()
        started = time.perf_counter()
        status_holder = {"status": 500}

        async def send_observed(message):
            if message["type"] == "http.response.start":
                status_holder["status"] = message["status"]
            await send(message)

        try:
            await self.app(scope, receive, send_observed)
        finally:
            registry.observe(
                scope.get("method", "GET"),
                status_holder["status"],
                scope.get("path", "/"),
                time.perf_counter() - started,
            )


class SecurityHeadersMiddleware:
    """Pure ASGI security-header middleware that streams responses without buffering."""

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)

        is_prod = os.getenv("APP_ENV") == "production"

        async def send_with_headers(message):
            if message["type"] == "http.response.start":
                raw_headers = list(message.get("headers", []))
                raw_headers.extend(
                    [
                        (b"x-content-type-options", b"nosniff"),
                        (b"x-frame-options", b"DENY"),
                        (b"referrer-policy", b"no-referrer"),
                        (b"cache-control", b"no-store"),
                    ]
                )
                if is_prod:
                    raw_headers.append((b"strict-transport-security", b"max-age=31536000; includeSubDomains"))
                message = {**message, "headers": raw_headers}
            await send(message)

        await self.app(scope, receive, send_with_headers)
