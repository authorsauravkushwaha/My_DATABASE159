"""Real Python → Go authorization → Go storage integration, including restart."""
import shutil
import socket
import subprocess
import sys
import threading
import time
from pathlib import Path

import httpx
import pytest
import uvicorn
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool
from test_api import auth, create_project, register

from app.db import Base, get_session
from app.main import app


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


@pytest.mark.skipif(shutil.which("go") is None, reason="Go toolchain not installed")
def test_real_gateway_authorizes_and_persists_records(tmp_path):
    root = Path(__file__).resolve().parents[1]
    binaries = []
    for module, package in (("engine", "./cmd/engine"), ("gateway", ".")):
        binary = tmp_path / module
        try:
            subprocess.run(
                ["go", "build", "-o", str(binary), package], cwd=root / module,
                check=True, capture_output=True, text=True, timeout=120,
            )
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
            pytest.fail(f"Could not build {module}: {exc}\n{exc.stderr or ''}")
        binaries.append(binary)
    engine_binary, gateway_binary = binaries
    api_port, engine_port, gateway_port = free_port(), free_port(), free_port()
    db = create_engine("sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(db)
    factory = sessionmaker(bind=db, expire_on_commit=False)

    def override():
        with factory() as session:
            yield session

    previous_overrides = app.dependency_overrides.copy()
    app.dependency_overrides[get_session] = override
    server = uvicorn.Server(uvicorn.Config(app, host="127.0.0.1", port=api_port, log_level="error"))
    thread = threading.Thread(target=server.run, daemon=True)
    thread.start()
    processes = []
    active_processes = set()

    def start_engine():
        log = tmp_path / f"engine-{len(processes)}.log"
        with log.open("w") as output:
            process = subprocess.Popen(
                [str(engine_binary), "--addr", f"127.0.0.1:{engine_port}", "--dir", str(tmp_path / "data")],
                stdout=output, stderr=subprocess.STDOUT,
            )
        processes.append(("engine", process, log))
        active_processes.add(process)
        return process

    def stop_engine(process):
        active_processes.remove(process)
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)

    def wait_ready(client):
        deadline = time.monotonic() + 15
        while True:
            for name, process, log in processes:
                if process in active_processes and process.poll() is not None:
                    raise RuntimeError(f"{name} exited with {process.returncode}: {log.read_text()}")
            if not thread.is_alive() or time.monotonic() > deadline:
                raise RuntimeError("Storage services did not become ready")
            try:
                with socket.create_connection(("127.0.0.1", engine_port), timeout=0.2):
                    pass
                if all(client.get(f"http://127.0.0.1:{port}/health").status_code == 200
                       for port in (api_port, gateway_port)):
                    return
            except (OSError, httpx.HTTPError):
                pass
            time.sleep(0.05)

    try:
        start_engine()
        log = tmp_path / "gateway.log"
        with log.open("w") as output:
            gateway = subprocess.Popen(
                [str(gateway_binary), "--addr", f"127.0.0.1:{gateway_port}",
                 "--storage", f"127.0.0.1:{engine_port}", "--control-plane", f"http://127.0.0.1:{api_port}"],
                stdout=output, stderr=subprocess.STDOUT,
            )
        processes.append(("gateway", gateway, log))
        active_processes.add(gateway)
        with httpx.Client(base_url=f"http://127.0.0.1:{gateway_port}", timeout=5, trust_env=False) as client:
            wait_ready(client)
            with TestClient(app) as api:
                token, org = register(client=api, email="storage@example.com")
                other, _ = register(client=api, email="storage-other@example.com")
                project = create_project(api, token, org)
                headers = auth(token)
                created = api.post(f"/v1/projects/{project}/collections", headers=headers, json={
                    "name": "notes", "schema": {"properties": {"title": {"type": "string"}}, "required": ["title"]},
                })
                assert created.status_code == 201, created.text
                collection = created.json()
                path = f"/v1/data/{project}/{collection['id']}"
                response = client.put(path + "/r1", headers=headers, json={"data": {"title": "real"}})
                assert response.status_code == 201, response.text
                assert client.get(path + "/r1", headers=headers).json()["data"] == {"title": "real"}
                assert client.get(path + "/r1", headers=auth(other)).status_code == 404
                assert client.put(path + "/bad", headers=headers, json={"data": {"title": 4}}).status_code == 422
                assert client.put(path + "/r2", headers=headers, json={"data": {"title": "second"}}).status_code == 201
                assert client.put(path + "/r3", headers=headers, json={"data": {"title": "third"}}).status_code == 201
                first = client.get(path + "?limit=1", headers=headers).json()
                assert first["records"][0]["id"] == "r1" and first["next_cursor"] == "r1"
                second = client.get(path + "?limit=1&after=r1", headers=headers).json()
                assert second["records"][0]["id"] == "r2" and second["next_cursor"] == "r2"
                last = client.get(path + "?limit=1&after=r2", headers=headers).json()
                assert last["records"][0]["id"] == "r3" and last["next_cursor"] == ""
                assert client.get(path + "?limit=101", headers=headers).status_code == 422
                assert client.post("/v1/scan", headers=headers, json={"prefix": ""}).status_code == 404
                api.patch(f"/v1/projects/{project}", headers=headers, json={"api_enabled": False})
                assert client.get(path + "/r1", headers=headers).status_code == 403
                api.patch(f"/v1/projects/{project}", headers=headers, json={"api_enabled": True})

                # SIGTERM does not run a graceful store close: acknowledged WAL
                # writes must survive, and the gateway must reconnect on retry.
                stop_engine(processes[0][1])
                restarted = start_engine()
                wait_ready(client)
                assert client.get(path + "/r1", headers=headers).json()["data"] == {"title": "real"}
                assert client.delete(path + "/r1", headers=headers).status_code == 204
                stop_engine(restarted)
                start_engine()
                wait_ready(client)
                assert client.get(path + "/r1", headers=headers).status_code == 404
                assert client.get(path + "/r2", headers=headers).json()["data"] == {"title": "second"}
    except BaseException as exc:
        logs = "\n".join(f"{name}:\n{log.read_text()}" for name, _, log in processes if log.exists())
        exc.add_note(f"Storage service logs:\n{logs}")
        raise
    finally:
        for _, process, _ in processes:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
        server.should_exit = True
        thread.join(timeout=5)
        if thread.is_alive():
            print("API test server did not stop within five seconds", file=sys.stderr)
        app.dependency_overrides.clear()
        app.dependency_overrides.update(previous_overrides)
        db.dispose()
