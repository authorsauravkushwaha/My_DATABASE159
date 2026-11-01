import os

os.environ["JWT_SECRET"] = "test-only-secret-that-is-at-least-32-bytes"

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, event, func, select
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from app.db import Base, get_session
from app.main import app
from app.models import Record


@pytest.fixture()
def client():
    engine = create_engine("sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool)

    @event.listens_for(engine, "connect")
    def enable_foreign_keys(connection, _record):
        cursor = connection.cursor()
        cursor.execute("PRAGMA foreign_keys=ON")
        cursor.close()

    Base.metadata.create_all(engine)
    testing_session = sessionmaker(bind=engine, autoflush=False, expire_on_commit=False)

    def override_session():
        session = testing_session()
        try:
            yield session
        finally:
            session.close()

    app.dependency_overrides[get_session] = override_session
    with TestClient(app) as test_client:
        test_client.testing_session = testing_session
        yield test_client
    app.dependency_overrides.clear()
    Base.metadata.drop_all(engine)
    engine.dispose()


def register(client, email="one@example.com"):
    response = client.post("/v1/auth/register", json={
        "email": email,
        "password": "correct-horse-battery-7!",
        "organization_name": "Acme Labs",
    })
    assert response.status_code == 201, response.text
    body = response.json()
    return body["access_token"], body["organization_id"]


def auth(token):
    return {"Authorization": f"Bearer {token}"}


def create_project(client, token, organization_id, slug="main"):
    response = client.post(f"/v1/organizations/{organization_id}/projects", headers=auth(token), json={"name": "Main", "slug": slug})
    assert response.status_code == 201, response.text
    return response.json()["id"]


def test_register_login_and_duplicate_email(client):
    token, organization_id = register(client)
    assert len(token) > 20
    assert client.post("/v1/auth/register", json={
        "email": "one@example.com", "password": "correct-horse-battery-7!", "organization_name": "Elsewhere"
    }).status_code == 409
    response = client.post("/v1/auth/login", json={"email": "one@example.com", "password": "correct-horse-battery-7!"})
    assert response.status_code == 200
    assert response.json()["organization_id"] == organization_id
    assert client.post("/v1/auth/login", json={"email": "one@example.com", "password": "incorrect password"}).status_code == 401


def test_collection_crud_and_validation(client):
    token, org = register(client)
    project_id = create_project(client, token, org)
    headers = auth(token)
    response = client.post(f"/v1/projects/{project_id}/collections", headers=headers, json={
        "name": "notes",
        "schema": {"type": "object", "properties": {"title": {"type": "string"}, "rank": {"type": "integer"}}, "required": ["title"]},
    })
    assert response.status_code == 201, response.text
    record = client.post(f"/v1/projects/{project_id}/data/notes", headers=headers, json={"data": {"title": "Hello", "rank": 3}})
    assert record.status_code == 201, record.text
    record_id = record.json()["id"]
    assert record.json()["data"] == {"title": "Hello", "rank": 3}
    assert client.get(f"/v1/projects/{project_id}/data/notes", headers=headers).json()[0]["id"] == record_id
    updated = client.patch(f"/v1/projects/{project_id}/data/notes/{record_id}", headers=headers, json={"data": {"title": "Updated"}})
    assert updated.status_code == 200
    assert updated.json()["data"] == {"title": "Updated", "rank": 3}
    assert client.delete(f"/v1/projects/{project_id}/data/notes/{record_id}", headers=headers).status_code == 204
    assert client.get(f"/v1/projects/{project_id}/data/notes", headers=headers).json() == []


def test_rejects_invalid_schema_and_record_payloads(client):
    token, org = register(client)
    project_id = create_project(client, token, org)
    headers = auth(token)
    bad_name = client.post(f"/v1/projects/{project_id}/collections", headers=headers, json={"name": "notes;drop_table", "schema": {}})
    assert bad_name.status_code == 422
    created = client.post(f"/v1/projects/{project_id}/collections", headers=headers, json={
        "name": "notes", "schema": {"properties": {"title": {"type": "string"}}, "required": ["title"]}
    })
    assert created.status_code == 201, created.text
    assert client.post(f"/v1/projects/{project_id}/data/notes", headers=headers, json={"data": {"other": "x"}}).status_code == 422
    assert client.post(f"/v1/projects/{project_id}/data/notes", headers=headers, json={"data": {"title": 4}}).status_code == 422
    assert client.post(f"/v1/projects/{project_id}/data/notes", headers=headers, json={"data": {"title": "x", "extra": True}}).status_code == 422


def test_tenant_isolation_and_authentication(client):
    token_a, org_a = register(client, "a@example.com")
    token_b, org_b = register(client, "b@example.com")
    project_a = create_project(client, token_a, org_a)
    assert client.get(f"/v1/organizations/{org_a}/projects", headers=auth(token_b)).status_code == 404
    assert client.get(f"/v1/projects/{project_a}", headers=auth(token_b)).status_code == 404
    assert client.post(f"/v1/organizations/{org_b}/projects", json={"name": "No auth", "slug": "no-auth"}).status_code == 401
    assert client.get(f"/v1/projects/{project_a}", headers={"Authorization": "Bearer invalid"}).status_code == 401


def test_pagination_bounds_and_project_api_toggle(client):
    token, org = register(client)
    project_id = create_project(client, token, org)
    headers = auth(token)
    client.post(f"/v1/projects/{project_id}/collections", headers=headers, json={"name": "items", "schema": {}})
    assert client.get(f"/v1/projects/{project_id}/data/items?limit=101", headers=headers).status_code == 422
    disabled = client.patch(f"/v1/projects/{project_id}", headers=headers, json={"api_enabled": False})
    assert disabled.status_code == 200 and disabled.json()["api_enabled"] is False
    assert client.get(f"/v1/projects/{project_id}/collections", headers=headers).status_code == 403
    assert client.patch(f"/v1/projects/{project_id}", headers=headers, json={"api_enabled": True}).json()["api_enabled"] is True


def test_records_are_scoped_and_project_delete_cascades(client):
    token, org = register(client)
    project_a = create_project(client, token, org, "project-a")
    project_b = create_project(client, token, org, "project-b")
    headers = auth(token)
    for project_id in (project_a, project_b):
        client.post(f"/v1/projects/{project_id}/collections", headers=headers, json={"name": "items", "schema": {}})
    record = client.post(f"/v1/projects/{project_a}/data/items", headers=headers, json={"data": {}}).json()
    assert client.patch(f"/v1/projects/{project_b}/data/items/{record['id']}", headers=headers, json={"data": {"value": "changed"}}).status_code == 404
    assert client.delete(f"/v1/projects/{project_a}", headers=headers).status_code == 204
    assert client.get(f"/v1/projects/{project_a}", headers=headers).status_code == 404
    with client.testing_session() as session:
        assert session.scalar(select(func.count()).select_from(Record).where(Record.project_id == project_a)) == 0
