"""Bound request bodies before JSON parsing, including chunked requests."""
from starlette.responses import JSONResponse


class RequestBodyLimitMiddleware:
    def __init__(self, app, max_bytes: int = 131_072):
        self.app = app
        self.max_bytes = max_bytes

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        headers = dict(scope.get("headers", []))
        try:
            declared = int(headers.get(b"content-length", b"0"))
        except ValueError:
            await JSONResponse({"detail": "Invalid Content-Length"}, status_code=400)(scope, receive, send)
            return
        too_large = JSONResponse({"detail": "Request body exceeds the 128 KiB limit"}, status_code=413)
        if declared > self.max_bytes:
            await too_large(scope, receive, send)
            return

        # Buffer at most the limit, then replay the request to the application.
        # This keeps framework exception handling out of the receive channel.
        messages, received = [], 0
        while True:
            message = await receive()
            messages.append(message)
            if message["type"] != "http.request":
                break
            received += len(message.get("body", b""))
            if received > self.max_bytes:
                await too_large(scope, receive, send)
                return
            if not message.get("more_body", False):
                break
        pending = iter(messages)

        async def replay_receive():
            return next(pending, {"type": "http.disconnect"})

        await self.app(scope, replay_receive, send)
