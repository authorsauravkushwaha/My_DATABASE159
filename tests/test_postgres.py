"""Run against a disposable PostgreSQL database in CI; never point at production."""
import os

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import sessionmaker
from test_api import auth, create_project, register

from app.db import get_session
from app.main import app


@pytest.mark.skipif(not os.getenv('TEST_DATABASE_URL'), reason='Disposable PostgreSQL URL not configured')
def test_postgresql_vertical_slice():
    url = os.environ['TEST_DATABASE_URL']
    assert url.startswith(('postgresql://', 'postgresql+psycopg://'))
    engine = create_engine(url)
    factory = sessionmaker(bind=engine, expire_on_commit=False)
    def override():
        with factory() as session:
            yield session
    app.dependency_overrides[get_session] = override
    try:
        with engine.connect() as connection:
            assert connection.execute(text("SELECT current_setting('server_version_num')")).scalar_one()
        with TestClient(app) as client:
            token, org = register(client, 'postgres@example.com')
            project = create_project(client, token, org)
            headers = auth(token)
            response = client.post(f'/v1/projects/{project}/collections', headers=headers, json={
                'name': 'notes', 'schema': {'properties': {'title': {'type': 'string'}}, 'required': ['title']},
            })
            assert response.status_code == 201, response.text
            record = client.post(f'/v1/projects/{project}/data/notes', headers=headers, json={'data': {'title': 'Persistent'}})
            assert record.status_code == 201, record.text
            records = client.get(f'/v1/projects/{project}/data/notes', headers=headers)
            assert records.json()[0]['data'] == {'title': 'Persistent'}
            batch = client.post(
                f'/v1/projects/{project}/data/notes/batch',
                headers=headers,
                json={'records': [{'title': ('Compressed-PostgreSQL-Payload-' * 6) + str(i)} for i in range(50)]},
            )
            assert batch.status_code == 201 and batch.json()['compression_ratio'] > 1.5
            assert client.post(f'/v1/projects/{project}/collections/notes/indexes', headers=headers, json={'field': 'title'}).status_code == 201
            queried = client.post(
                f'/v1/projects/{project}/data/notes/query',
                headers=headers,
                json={'filters': [{'field': 'title', 'op': 'eq', 'value': 'Persistent'}]},
            )
            assert queried.status_code == 200 and queried.json()['index_used'] is True and queried.json()['returned'] == 1
            exported = client.get(f'/v1/projects/{project}/data/notes/export', headers=headers)
            assert exported.status_code == 200 and len(exported.text.strip().splitlines()) == 51
            assert client.delete(f'/v1/projects/{project}', headers=headers).status_code == 204
    finally:
        app.dependency_overrides.clear()
        engine.dispose()
