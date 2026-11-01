import json
import os
import shutil
import socket
import subprocess
import time
from pathlib import Path

import httpx
import pytest
import uvicorn
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool
from test_api import auth, create_project, register

from app.db import Base, get_session
from app.main import app
from app.models import Membership, Record, User


def test_transparent_compression_batch_ingest_and_storage_stats(client):
    token, org = register(client)
    project_id = create_project(client, token, org)
    headers = auth(token)

    created = client.post(
        f"/v1/projects/{project_id}/collections",
        headers=headers,
        json={
            "name": "events",
            "schema": {
                "type": "object",
                "properties": {
                    "event_type": {"type": "string"},
                    "region": {"type": "string"},
                    "latency_ms": {"type": "integer"},
                    "billable": {"type": "boolean"},
                    "payload_summary": {"type": "string"},
                },
                "required": ["event_type", "latency_ms"],
                "indexes": ["event_type", "latency_ms"],
            },
        },
    )
    assert created.status_code == 201, created.text
    assert created.json()["schema"]["indexes"] == ["event_type", "latency_ms"]

    records = [
        {
            "event_type": "order.created" if i % 3 == 0 else "page.viewed",
            "region": "ap-south-1" if i % 2 == 0 else "eu-central-1",
            "latency_ms": (i * 7) % 500,
            "billable": i % 2 == 0,
            "payload_summary": ("structured-event-telemetry-record-with-repeatable-tokens-" * 6) + str(i),
        }
        for i in range(600)
    ]
    batch_resp = client.post(
        f"/v1/projects/{project_id}/data/events/batch",
        headers=headers,
        json={"records": records},
    )
    assert batch_resp.status_code == 201, batch_resp.text
    body = batch_resp.json()
    assert body["inserted"] == 600
    assert body["compression_ratio"] >= 2.5
    assert body["saved_bytes"] > 100_000

    # Confirm physical rows are stored compressed without duplicate inline JSON bloat.
    with client.testing_session() as session:
        sample = session.scalar(select(Record).where(Record.project_id == project_id).limit(1))
        assert sample is not None
        assert sample.compressed_data is not None
        assert sample.inline_data is None
        assert sample.stored_size_bytes < sample.raw_size_bytes

    stats = client.get(f"/v1/projects/{project_id}/storage", headers=headers)
    assert stats.status_code == 200, stats.text
    sbody = stats.json()
    assert sbody["total_records"] == 600
    assert sbody["compressed_records"] == 600
    assert sbody["compression_ratio"] >= 2.5
    assert sbody["indexed_fields_count"] == 2
    assert sbody["active_shards"] >= 32


def test_secondary_btree_indexes_query_pushdown_and_cursor_pagination(client):
    token, org = register(client)
    project_id = create_project(client, token, org)
    headers = auth(token)

    client.post(
        f"/v1/projects/{project_id}/collections",
        headers=headers,
        json={
            "name": "catalog",
            "schema": {
                "type": "object",
                "properties": {
                    "sku": {"type": "string"},
                    "category": {"type": "string"},
                    "price": {"type": "number"},
                    "active": {"type": "boolean"},
                },
                "required": ["sku", "price"],
            },
        },
    )
    items = [
        {
            "sku": f"SKU-{i:04d}",
            "category": "audio" if i % 4 == 0 else "compute",
            "price": float(i * 10),
            "active": i % 2 == 0,
        }
        for i in range(1, 201)
    ]
    assert client.post(f"/v1/projects/{project_id}/data/catalog/batch", headers=headers, json={"records": items}).status_code == 201

    # Online index build backfills all 200 existing records.
    idx_resp = client.post(f"/v1/projects/{project_id}/collections/catalog/indexes", headers=headers, json={"field": "price"})
    assert idx_resp.status_code == 201, idx_resp.text
    assert idx_resp.json()["indexed_entries"] == 200

    idx_cat = client.post(f"/v1/projects/{project_id}/collections/catalog/indexes", headers=headers, json={"field": "category"})
    assert idx_cat.status_code == 201

    # Query using both secondary indexes + field projection + cursor pagination.
    q1 = client.post(
        f"/v1/projects/{project_id}/data/catalog/query",
        headers=headers,
        json={
            "filters": [
                {"field": "category", "op": "eq", "value": "audio"},
                {"field": "price", "op": "gte", "value": 1000},
            ],
            "select": ["sku", "price"],
            "limit": 10,
        },
    )
    assert q1.status_code == 200, q1.text
    q1_body = q1.json()
    assert q1_body["index_used"] is True
    assert set(q1_body["indexed_fields_used"]) == {"category", "price"}
    assert q1_body["returned"] == 10
    assert q1_body["next_cursor"] is not None
    assert set(q1_body["records"][0]["data"].keys()) == {"sku", "price"}
    assert all(r["data"]["price"] >= 1000 for r in q1_body["records"])

    # Walk remaining pages via cursor.
    collected = list(q1_body["records"])
    cursor = q1_body["next_cursor"]
    while cursor:
        nxt = client.post(
            f"/v1/projects/{project_id}/data/catalog/query",
            headers=headers,
            json={
                "filters": [
                    {"field": "category", "op": "eq", "value": "audio"},
                    {"field": "price", "op": "gte", "value": 1000},
                ],
                "limit": 10,
                "after": cursor,
            },
        ).json()
        collected.extend(nxt["records"])
        cursor = nxt["next_cursor"]

    # Multiples of 4 in 1..200 with i*10 >= 1000 => i in {100, 104, ..., 200} => 26 items.
    assert len(collected) == 26

    # Drop secondary index cleanly.
    assert client.delete(f"/v1/projects/{project_id}/collections/catalog/indexes/category", headers=headers).status_code == 204


def test_streaming_ndjson_export_import_and_keyset_list(client):
    token, org = register(client)
    project_id = create_project(client, token, org)
    headers = auth(token)

    client.post(
        f"/v1/projects/{project_id}/collections",
        headers=headers,
        json={
            "name": "logs",
            "schema": {
                "properties": {"msg": {"type": "string"}, "seq": {"type": "integer"}},
                "required": ["msg", "seq"],
            },
        },
    )
    ndjson_input = "\n".join(json.dumps({"msg": f"entry-{i}", "seq": i}) for i in range(250))
    imp = client.post(
        f"/v1/projects/{project_id}/data/logs/import",
        headers={**headers, "Content-Type": "application/x-ndjson"},
        content=ndjson_input.encode("utf-8"),
    )
    assert imp.status_code == 201, imp.text
    assert imp.json()["inserted"] == 250

    # Keyset cursor pagination on GET list_records.
    page1 = client.get(f"/v1/projects/{project_id}/data/logs?limit=100", headers=headers)
    assert page1.status_code == 200
    assert len(page1.json()) == 100
    c1 = page1.headers.get("x-next-cursor")
    assert c1 == page1.json()[-1]["id"]

    page2 = client.get(f"/v1/projects/{project_id}/data/logs?limit=100&after={c1}", headers=headers)
    assert len(page2.json()) == 100
    c2 = page2.headers.get("x-next-cursor")
    page3 = client.get(f"/v1/projects/{project_id}/data/logs?limit=100&after={c2}", headers=headers)
    assert len(page3.json()) == 50
    assert "x-next-cursor" not in page3.headers

    # Full-collection streaming NDJSON export exceeds single-page 100 limit.
    exported = client.get(f"/v1/projects/{project_id}/data/logs/export", headers=headers)
    assert exported.status_code == 200
    lines = [json.loads(line) for line in exported.text.strip().splitlines()]
    assert len(lines) == 250
    assert lines[0]["data"]["seq"] == 0
    assert lines[-1]["data"]["seq"] == 249


def test_high_capacity_endpoints_enforce_roles_and_tenant_isolation(client):
    owner_token, org = register(client, "hc-owner@example.com")
    viewer_token, _ = register(client, "hc-viewer@example.com")
    outsider_token, _ = register(client, "hc-outsider@example.com")
    project_id = create_project(client, owner_token, org)

    with client.testing_session() as session:
        viewer_user = session.scalar(select(User).where(User.email == "hc-viewer@example.com"))
        session.add(Membership(organization_id=org, user_id=viewer_user.id, role="viewer"))
        session.commit()

    client.post(
        f"/v1/projects/{project_id}/collections",
        headers=auth(owner_token),
        json={"name": "items", "schema": {"properties": {"name": {"type": "string"}}, "required": ["name"]}},
    )

    # Viewer can read storage stats, query, and export, but cannot batch-write, import, index, or compact.
    assert client.get(f"/v1/projects/{project_id}/storage", headers=auth(viewer_token)).status_code == 200
    assert client.post(f"/v1/projects/{project_id}/data/items/query", headers=auth(viewer_token), json={}).status_code == 200
    assert client.get(f"/v1/projects/{project_id}/data/items/export", headers=auth(viewer_token)).status_code == 200
    assert client.post(f"/v1/projects/{project_id}/data/items/batch", headers=auth(viewer_token), json={"records": [{"name": "x"}]}).status_code == 403
    assert client.post(f"/v1/projects/{project_id}/data/items/import", headers=auth(viewer_token), content=b'{"name":"x"}').status_code == 403
    assert client.post(f"/v1/projects/{project_id}/collections/items/indexes", headers=auth(viewer_token), json={"field": "name"}).status_code == 403
    assert client.post(f"/v1/projects/{project_id}/collections/items/compact", headers=auth(viewer_token)).status_code == 403

    # Outsider gets 404 across all project endpoints.
    assert client.get(f"/v1/projects/{project_id}/storage", headers=auth(outsider_token)).status_code == 404
    assert client.post(f"/v1/projects/{project_id}/data/items/query", headers=auth(outsider_token), json={}).status_code == 404


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


@pytest.mark.skipif(shutil.which("go") is None, reason="Go toolchain not installed")
def test_gateway_batch_and_control_plane_orphan_cleanup(tmp_path):
    root = Path(__file__).resolve().parents[1]
    engine_bin = tmp_path / "engine"
    gateway_bin = tmp_path / "gateway"
    subprocess.run(["go", "build", "-o", str(engine_bin), "./cmd/engine"], cwd=root / "engine", check=True, timeout=120)
    subprocess.run(["go", "build", "-o", str(gateway_bin), "."], cwd=root / "gateway", check=True, timeout=120)

    api_port, engine_port, gateway_port = _free_port(), _free_port(), _free_port()
    db = create_engine("sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(db)
    factory = sessionmaker(bind=db, expire_on_commit=False)

    def override():
        with factory() as s:
            yield s

    prev_overrides = app.dependency_overrides.copy()
    prev_engine_env = os.environ.get("STORAGE_ENGINE_ADDR")
    os.environ["STORAGE_ENGINE_ADDR"] = f"127.0.0.1:{engine_port}"
    app.dependency_overrides[get_session] = override

    import threading

    server = uvicorn.Server(uvicorn.Config(app, host="127.0.0.1", port=api_port, log_level="error"))
    thread = threading.Thread(target=server.run, daemon=True)
    thread.start()

    eng_proc = subprocess.Popen([str(engine_bin), "--addr", f"127.0.0.1:{engine_port}", "--dir", str(tmp_path / "lsm"), "--shards", "2"])
    gw_proc = subprocess.Popen([
        str(gateway_bin),
        "--addr", f"127.0.0.1:{gateway_port}",
        "--storage", f"127.0.0.1:{engine_port}",
        "--control-plane", f"http://127.0.0.1:{api_port}",
    ])

    try:
        with httpx.Client(base_url=f"http://127.0.0.1:{gateway_port}", timeout=5, trust_env=False) as gw:
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                try:
                    if gw.get("/health").status_code == 200 and httpx.get(f"http://127.0.0.1:{api_port}/health", trust_env=False).status_code == 200:
                        break
                except httpx.HTTPError:
                    pass
                time.sleep(0.05)

            with TestClient(app) as api:
                token, org = register(api, "bridge@example.com")
                project = create_project(api, token, org)
                headers = auth(token)
                coll = api.post(
                    f"/v1/projects/{project}/collections",
                    headers=headers,
                    json={"name": "telemetry", "schema": {"properties": {"host": {"type": "string"}}, "required": ["host"]}},
                ).json()

                # Batch-write 200 records through the Go gateway into the 2-shard LSM engine.
                batch_payload = {"records": [{"id": f"rec-{i:04d}", "data": {"host": f"node-{i}"}} for i in range(200)]}
                b_res = gw.post(f"/v1/data/{project}/{coll['id']}/batch", headers=headers, json=batch_payload)
                assert b_res.status_code == 201, b_res.text
                assert b_res.json()["written"] == 200

                # Project storage endpoint reports live sharded Go engine metrics!
                st_res = api.get(f"/v1/projects/{project}/storage", headers=headers)
                assert st_res.status_code == 200
                assert st_res.json()["engine"]["shards"] == 2

                # Delete collection in Python control plane -> automatically purges Go LSM namespace!
                assert api.delete(f"/v1/projects/{project}/collections/telemetry", headers=headers).status_code == 204

                # Verify engine keys were actually erased on disk.
                from app.compression import _engine_call
                scan_after_delete = _engine_call({"op": "scan", "prefix": "", "limit": 10})
                assert scan_after_delete is not None and scan_after_delete.get("kvs", []) == []
    finally:
        for p in (gw_proc, eng_proc):
            if p.poll() is None:
                p.terminate()
                p.wait(timeout=5)
        server.should_exit = True
        thread.join(timeout=5)
        app.dependency_overrides.clear()
        app.dependency_overrides.update(prev_overrides)
        if prev_engine_env is None:
            os.environ.pop("STORAGE_ENGINE_ADDR", None)
        else:
            os.environ["STORAGE_ENGINE_ADDR"] = prev_engine_env
        db.dispose()
