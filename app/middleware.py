"""Bound request bodies before JSON parsing, including chunked requests.

Control-plane auth/admin routes stay strictly capped at 128 KiB, single-record
and query routes allow up to 1 MiB, and bulk batch/NDJSON dataset ingestion
routes allow up to 16 MiB.
"""
import os

from starlette.responses import JSONResponse

DEFAULT_MAX_BYTES = 131_072  # 128 KiB
DATA_ROUTE_MAX_BYTES = 1_048_576  # 1 MiB
BULK_ROUTE_MAX_BYTES = int(os.getenv("MAX_BULK_BODY_BYTES", str(16 * 1024 * 1024)))  # 16 MiB


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
