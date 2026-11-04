"""Dependency-free Prometheus text-format metrics with bounded label cardinality.

Tracks HTTP request counters and latency histograms keyed by a *normalized* path:
UUID segments and user-defined collection names are folded into placeholders, and
the number of distinct path patterns is capped so hostile traffic cannot grow the
registry without bound.
"""
from __future__ import annotations

import re
import threading

REQUEST_DURATION_BUCKETS = (0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0)
MAX_PATH_PATTERNS = 200
OTHER_PATH = "other"

_UUID_SEGMENT = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
# Segments that follow a parent resource and may be user-defined (collection names).
_NAME_AFTER = {"data", "collections"}
_NAME_KEEP = {"batch", "import", "export", "query", "indexes", "compact", "access", "storage"}

CounterKey = tuple[str, str, str]  # (method, status, path)


def normalize_path(path: str) -> str:
    """Collapse tenant identifiers into stable placeholders for metric labels."""
    segments = [segment for segment in path.split("/") if segment]
    parts: list[str] = []
    for index, segment in enumerate(segments):
        if _UUID_SEGMENT.match(segment):
            parts.append("{id}")
            continue
        previous = segments[index - 1] if index else None
        if previous in _NAME_AFTER and segment not in _NAME_KEEP:
            parts.append("{name}")
            continue
        parts.append(segment)
    return "/" + "/".join(parts) if parts else "/"


def _escape_label(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n")


class MetricsRegistry:
    def __init__(self, max_path_patterns: int = MAX_PATH_PATTERNS, version: str = "dev"):
        self._lock = threading.Lock()
        self._max_path_patterns = max_path_patterns
        self._version = version
        self._seen_patterns: set[str] = set()
        self._counters: dict[CounterKey, int] = {}
        # path -> [bucket counts..., sum, count]
        self._histograms: dict[str, list[float]] = {}

    def _bounded_path(self, path: str) -> str:
        if path in self._seen_patterns:
            return path
        if len(self._seen_patterns) >= self._max_path_patterns:
            return OTHER_PATH
        self._seen_patterns.add(path)
        return path

    def observe(self, method: str, status: int, path: str, duration_seconds: float) -> None:
        normalized = normalize_path(path)
        with self._lock:
            bounded = self._bounded_path(normalized)
            counter_key = (method, str(status), bounded)
            self._counters[counter_key] = self._counters.get(counter_key, 0) + 1
            buckets = self._histograms.get(bounded)
            if buckets is None:
                buckets = [0.0] * (len(REQUEST_DURATION_BUCKETS) + 2)
                self._histograms[bounded] = buckets
            for index, upper in enumerate(REQUEST_DURATION_BUCKETS):
                if duration_seconds <= upper:
                    buckets[index] += 1
            buckets[-2] += duration_seconds
            buckets[-1] += 1

    def render(self) -> str:
        with self._lock:
            lines = [
                "# HELP kestrel_build_info Build metadata for the Kestrel API process.",
                "# TYPE kestrel_build_info gauge",
                f'kestrel_build_info{{version="{_escape_label(self._version)}"}} 1',
                "# HELP kestrel_http_requests_total Total HTTP requests handled by the Kestrel API.",
                "# TYPE kestrel_http_requests_total counter",
            ]
            for (method, status, path), count in sorted(self._counters.items()):
                lines.append(
                    f'kestrel_http_requests_total{{method="{_escape_label(method)}",'
                    f'status="{_escape_label(status)}",path="{_escape_label(path)}"}} {count}'
                )
            lines.append("# HELP kestrel_http_request_duration_seconds Request latency in seconds.")
            lines.append("# TYPE kestrel_http_request_duration_seconds histogram")
            for path, buckets in sorted(self._histograms.items()):
                label_path = _escape_label(path)
                for index, upper in enumerate(REQUEST_DURATION_BUCKETS):
                    lines.append(
                        f'kestrel_http_request_duration_seconds_bucket{{path="{label_path}",le="{upper}"}} {int(buckets[index])}'
                    )
                lines.append(f'kestrel_http_request_duration_seconds_bucket{{path="{label_path}",le="+Inf"}} {int(buckets[-1])}')
                lines.append(f'kestrel_http_request_duration_seconds_sum{{path="{label_path}"}} {buckets[-2]:.6f}')
                lines.append(f'kestrel_http_request_duration_seconds_count{{path="{label_path}"}} {int(buckets[-1])}')
            return "\n".join(lines) + "\n"


registry: MetricsRegistry | None = None


def get_registry() -> MetricsRegistry:
    global registry
    if registry is None:
        registry = MetricsRegistry()
    return registry


def reset_registry(version: str = "dev") -> MetricsRegistry:
    """Replace the process registry (used by tests to start from a clean state)."""
    global registry
    registry = MetricsRegistry(version=version)
    return registry
