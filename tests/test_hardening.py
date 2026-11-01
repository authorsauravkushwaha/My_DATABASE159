from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from test_api import auth, create_project, register

from app.db import get_session
from app.main import app
from app.models import Membership


def test_gateway_access_is_membership_and_collection_scoped(client):
    token, org = register(client)
    other, _ = register(client, 'other@example.com')
    project = create_project(client, token, org)
    collection = client.post(f'/v1/projects/{project}/collections', headers=auth(token), json={'name': 'notes', 'schema': {}}).json()
    path = f"/v1/projects/{project}/collections/{collection['id']}/access"
    assert client.get(path).status_code == 401
    assert client.get(path, headers=auth(other)).status_code == 404
    result = client.get(path, headers=auth(token))
    assert result.status_code == 200
    assert result.json()['collection_id'] == collection['id']
    assert client.get(path + '?write=true', headers=auth(token)).status_code == 200
    client.patch(f'/v1/projects/{project}', headers=auth(token), json={'api_enabled': False})
    assert client.get(path, headers=auth(token)).status_code == 403


def test_viewer_is_read_only(client):
    token, org = register(client)
    viewer, _ = register(client, 'viewer@example.com')
    project = create_project(client, token, org)
    with client.testing_session() as session:
        from sqlalchemy import select

        from app.models import User
        user = session.scalar(select(User).where(User.email == 'viewer@example.com'))
        session.add(Membership(organization_id=org, user_id=user.id, role='viewer'))
        session.commit()
    headers = auth(viewer)
    assert client.get(f'/v1/projects/{project}/collections', headers=headers).status_code == 200
    assert client.post(f'/v1/projects/{project}/collections', headers=headers, json={'name': 'notes', 'schema': {}}).status_code == 403
    client.post(f'/v1/projects/{project}/collections', headers=auth(token), json={'name': 'notes', 'schema': {}})
    assert client.post(f'/v1/projects/{project}/data/notes', headers=headers, json={'data': {}}).status_code == 403


def test_collection_delete_cascades_and_is_scoped(client):
    token, org = register(client)
    project = create_project(client, token, org)
    other = create_project(client, token, org, 'other')
    headers = auth(token)
    client.post(f'/v1/projects/{project}/collections', headers=headers, json={'name': 'notes', 'schema': {}})
    client.post(f'/v1/projects/{other}/collections', headers=headers, json={'name': 'notes', 'schema': {}})
    client.post(f'/v1/projects/{project}/data/notes', headers=headers, json={'data': {}})
    assert client.delete(f'/v1/projects/{project}/collections/notes', headers=headers).status_code == 204
    assert client.get(f'/v1/projects/{project}/collections', headers=headers).json() == []
    assert len(client.get(f'/v1/projects/{other}/collections', headers=headers).json()) == 1


def test_readiness_and_security_headers(client):
    assert client.get('/health').json()['status'] == 'ok'
    response = client.get('/ready')
    assert response.status_code == 200
    assert response.json()['database'] == 'ready'
    assert response.headers['x-content-type-options'] == 'nosniff'
    assert response.headers['cache-control'] == 'no-store'


def test_readiness_fails_closed():
    broken = create_engine('sqlite:///file:nonexistent-database?mode=ro&uri=true')
    session_factory = sessionmaker(bind=broken)
    def session():
        with session_factory() as s:
            yield s
    app.dependency_overrides[get_session] = session
    try:
        with TestClient(app) as cli:
            assert cli.get('/ready').status_code == 503
            assert cli.get('/health').status_code == 200
    finally:
        app.dependency_overrides.clear()
        broken.dispose()


def test_request_body_limit_declared_and_streamed(client):
    response = client.post('/v1/auth/login', content=b'x', headers={'Content-Length': '131073'})
    assert response.status_code == 413
    def chunks():
        yield b'x' * 65536
        yield b'x' * 65537
    response = client.post('/v1/auth/login', content=chunks(), headers={'Content-Type': 'application/json'})
    assert response.status_code == 413
