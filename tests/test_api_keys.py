import hashlib
from datetime import datetime, timedelta, timezone

from sqlalchemy import select
from test_api import auth, create_project, register

from app.models import ApiKey, Membership


def setup_workspace(client, email="keys@example.com", slug="main"):
    token, org = register(client, email)
    project = create_project(client, token, org, slug)
    headers = auth(token)
    created = client.post(
        f"/v1/projects/{project}/collections",
        headers=headers,
        json={"name": "items", "schema": {"properties": {"title": {"type": "string"}}, "required": ["title"]}},
    )
    assert created.status_code == 201, created.text
    return token, org, project, headers


def make_key(client, project, headers, name="CI writer", role="writer", **extra):
    response = client.post(
        f"/v1/projects/{project}/api-keys",
        headers=headers,
        json={"name": name, "role": role, **extra},
    )
    assert response.status_code == 201, response.text
    return response.json()


def key_headers(key):
    return {"Authorization": f"Bearer {key['api_key']}"}


def test_api_key_lifecycle_secret_shown_once_and_stored_as_digest(client):
    token, org, project, headers = setup_workspace(client)
    created = make_key(client, project, headers, name="Nightly ingest", role="writer")
    secret = created["api_key"]
    assert secret.startswith("klr_") and len(secret) == 53
    assert created["key_prefix"] == secret.split("_")[1]
    assert created["revoked_at"] is None and created["last_used_at"] is None

    # The full secret is never retrievable again; the list only exposes metadata + prefix.
    listing = client.get(f"/v1/projects/{project}/api-keys", headers=headers)
    assert listing.status_code == 200
    assert [k["id"] for k in listing.json()] == [created["id"]]
    assert "api_key" not in listing.json()[0]
    assert listing.json()[0]["key_prefix"] == created["key_prefix"]

    # Persistence holds only a SHA-256 digest of the secret, never the secret itself.
    with client.testing_session() as session:
        row = session.get(ApiKey, created["id"])
        assert row.key_hash == hashlib.sha256(secret.encode()).hexdigest()
        assert secret not in row.key_hash

    # The key authenticates the full data plane and touches last_used_at.
    record = client.post(f"/v1/projects/{project}/data/items", headers=key_headers(created), json={"data": {"title": "From a key"}})
    assert record.status_code == 201, record.text
    listing = client.get(f"/v1/projects/{project}/api-keys", headers=headers).json()
    assert listing[0]["last_used_at"] is not None

    # Revocation takes effect immediately and is idempotent.
    assert client.delete(f"/v1/projects/{project}/api-keys/{created['id']}", headers=headers).status_code == 204
    assert client.get(f"/v1/projects/{project}/data/items", headers=key_headers(created)).status_code == 401
    assert client.delete(f"/v1/projects/{project}/api-keys/{created['id']}", headers=headers).status_code == 204
    listing = client.get(f"/v1/projects/{project}/api-keys", headers=headers).json()
    assert listing[0]["revoked_at"] is not None


def test_reader_key_is_read_only_across_the_data_plane(client):
    token, org, project, headers = setup_workspace(client)
    client.post(f"/v1/projects/{project}/data/items", headers=headers, json={"data": {"title": "Seed"}})
    reader = make_key(client, project, headers, name="Reader", role="reader")
    as_key = key_headers(reader)

    # Reads (including POST /query, which is a read) are allowed.
    assert client.get(f"/v1/projects/{project}/data/items", headers=as_key).status_code == 200
    assert client.get(f"/v1/projects/{project}/collections", headers=as_key).status_code == 200
    query = client.post(
        f"/v1/projects/{project}/data/items/query",
        headers=as_key,
        json={"filters": [{"field": "title", "op": "eq", "value": "Seed"}]},
    )
    assert query.status_code == 200 and query.json()["returned"] == 1
    export = client.get(f"/v1/projects/{project}/data/items/export", headers=as_key)
    assert export.status_code == 200 and "Seed" in export.text

    # Every write is refused with an explicit read-only explanation.
    writes = [
        client.post(f"/v1/projects/{project}/data/items", headers=as_key, json={"data": {"title": "x"}}),
        client.post(f"/v1/projects/{project}/data/items/batch", headers=as_key, json={"records": [{"title": "x"}]}),
        client.post(
            f"/v1/projects/{project}/data/items/import",
            headers={**as_key, "Content-Type": "application/x-ndjson"},
            content=b'{"title":"x"}\n',
        ),
        client.post(f"/v1/projects/{project}/collections", headers=as_key, json={"name": "extra", "schema": {}}),
        client.post(f"/v1/projects/{project}/collections/items/indexes", headers=as_key, json={"field": "title"}),
        client.post(f"/v1/projects/{project}/collections/items/compact", headers=as_key),
    ]
    for response in writes:
        assert response.status_code == 403, response.text
        assert "read-only" in response.json()["detail"]


def test_writer_key_has_full_data_plane_but_no_administration(client):
    token, org, project, headers = setup_workspace(client)
    other_project = create_project(client, token, org, "second")
    writer = make_key(client, project, headers, name="Writer", role="writer")
    as_key = key_headers(writer)

    created = client.post(f"/v1/projects/{project}/data/items", headers=as_key, json={"data": {"title": "Bulk"}})
    assert created.status_code == 201
    batch = client.post(f"/v1/projects/{project}/data/items/batch", headers=as_key, json={"records": [{"title": "a"}, {"title": "b"}]})
    assert batch.status_code == 201 and batch.json()["inserted"] == 2
    record_id = created.json()["id"]
    assert client.patch(f"/v1/projects/{project}/data/items/{record_id}", headers=as_key, json={"data": {"title": "Edited"}}).status_code == 200
    assert client.post(f"/v1/projects/{project}/collections/items/indexes", headers=as_key, json={"field": "title"}).status_code == 201
    assert client.post(f"/v1/projects/{project}/collections/items/compact", headers=as_key).status_code == 200
    assert client.get(f"/v1/projects/{project}/storage", headers=as_key).status_code == 200

    # Control-plane administration always requires an interactive user session.
    admin_calls = [
        client.patch(f"/v1/projects/{project}", headers=as_key, json={"api_enabled": False}),
        client.delete(f"/v1/projects/{project}", headers=as_key),
        client.get(f"/v1/organizations/{org}/projects", headers=as_key),
        client.post(f"/v1/organizations/{org}/projects", headers=as_key, json={"name": "Nope", "slug": "nope"}),
        client.get(f"/v1/projects/{project}/api-keys", headers=as_key),
        client.post(f"/v1/projects/{project}/api-keys", headers=as_key, json={"name": "nested", "role": "writer"}),
    ]
    for response in admin_calls:
        assert response.status_code == 403, response.text

    # A different project inside the same organization stays invisible to the key.
    assert client.get(f"/v1/projects/{other_project}", headers=as_key).status_code == 404
    assert client.get(f"/v1/projects/{other_project}/collections", headers=as_key).status_code == 404


def test_api_key_is_single_project_scoped(client):
    token, org, project, headers = setup_workspace(client)
    other_project = create_project(client, token, org, "other")
    client.post(f"/v1/projects/{other_project}/collections", headers=headers, json={"name": "secrets", "schema": {}})
    key = make_key(client, project, headers, name="Scoped")

    assert client.get(f"/v1/projects/{project}/collections", headers=key_headers(key)).status_code == 200
    for response in [
        client.get(f"/v1/projects/{other_project}/data/secrets", headers=key_headers(key)),
        client.post(f"/v1/projects/{other_project}/data/secrets", headers=key_headers(key), json={"data": {}}),
        client.get(f"/v1/projects/{other_project}/storage", headers=key_headers(key)),
        client.post(f"/v1/projects/{other_project}/api-keys", headers=key_headers(key), json={"name": "cross"}),
    ]:
        assert response.status_code == 404, response.text


def test_api_key_rejection_paths(client):
    token, org, project, headers = setup_workspace(client)
    key = make_key(client, project, headers, name="Temporary", role="writer")
    as_key = key_headers(key)
    assert client.get(f"/v1/projects/{project}/collections", headers=as_key).status_code == 200

    # Malformed, unknown, and non-key bearer tokens all fail closed with 401.
    for bad in ("klr_not-a-real-key", "klr_" + "a" * 16 + "_" + "b" * 32, "totally-invalid"):
        response = client.get(f"/v1/projects/{project}/collections", headers=auth(bad))
        assert response.status_code == 401, bad
        assert response.headers.get("www-authenticate") == "Bearer"

    # Expiry is enforced even if the row was valid moments earlier.
    with client.testing_session() as session:
        row = session.get(ApiKey, key["id"])
        row.expires_at = datetime.now(timezone.utc) - timedelta(seconds=1)
        session.commit()
    assert client.get(f"/v1/projects/{project}/collections", headers=as_key).status_code == 401

    # The creator's JWT is unaffected by key expiry.
    assert client.get(f"/v1/projects/{project}/collections", headers=headers).status_code == 200

    # A well-formed JWT that is not an access token is rejected (type=access is required).
    import jwt as pyjwt

    from app.security import ALGORITHM, _jwt_secret

    wrong_type = pyjwt.encode(
        {"sub": "someone", "iat": datetime.now(timezone.utc), "exp": datetime.now(timezone.utc) + timedelta(minutes=5), "type": "refresh"},
        _jwt_secret(),
        algorithm=ALGORITHM,
    )
    assert client.get(f"/v1/projects/{project}/collections", headers=auth(wrong_type)).status_code == 401


def test_api_key_never_outlives_creator_membership(client):
    token, org, project, headers = setup_workspace(client)
    key = make_key(client, project, headers, name="Member-bound", role="writer")
    assert client.get(f"/v1/projects/{project}/data/items", headers=key_headers(key)).status_code == 200
    with client.testing_session() as session:
        membership = session.scalar(select(Membership).where(Membership.organization_id == org))
        session.delete(membership)
        session.commit()
    # Membership gone: the key cannot see anything in the organization anymore.
    assert client.get(f"/v1/projects/{project}/data/items", headers=key_headers(key)).status_code == 404
    assert client.get(f"/v1/projects/{project}/data/items", headers=headers).status_code == 404


def test_api_key_management_requires_project_admin(client):
    token, org, project, headers = setup_workspace(client)
    editor, _ = register(client, "editor@example.com")
    with client.testing_session() as session:
        from app.models import User

        user = session.scalar(select(User).where(User.email == "editor@example.com"))
        session.add(Membership(organization_id=org, user_id=user.id, role="editor"))
        session.commit()
    editor_headers = auth(editor)
    # Editors hold data-plane write rights but cannot mint credentials.
    response = client.post(
        f"/v1/projects/{project}/api-keys",
        headers=editor_headers,
        json={"name": "Editor key", "role": "writer"},
    )
    assert response.status_code == 403
    assert client.get(f"/v1/projects/{project}/api-keys", headers=editor_headers).status_code == 403
    # Viewers cannot use keys they could never create either (no write role, no management).
    key = make_key(client, project, headers, name="Owner key")
    assert client.delete(f"/v1/projects/{project}/api-keys/{key['id']}", headers=editor_headers).status_code == 403


def test_api_key_authorizes_the_gateway_access_endpoint(client):
    token, org, project, headers = setup_workspace(client)
    collection = client.get(f"/v1/projects/{project}/collections", headers=headers).json()[0]
    access_path = f"/v1/projects/{project}/collections/{collection['id']}/access"
    reader = make_key(client, project, headers, name="Gateway reader", role="reader")
    writer = make_key(client, project, headers, name="Gateway writer", role="writer")

    assert client.get(access_path, headers=key_headers(reader)).status_code == 200
    assert client.get(access_path + "?write=true", headers=key_headers(reader)).status_code == 403
    assert client.get(access_path + "?write=true", headers=key_headers(writer)).status_code == 200


def test_api_key_validation_and_caps(client):
    token, org, project, headers = setup_workspace(client)
    # Role and expiry bounds are validated.
    assert client.post(f"/v1/projects/{project}/api-keys", headers=headers, json={"name": "x", "role": "admin"}).status_code == 422
    assert client.post(f"/v1/projects/{project}/api-keys", headers=headers, json={"name": "x", "expires_in_days": 0}).status_code == 422
    assert client.post(f"/v1/projects/{project}/api-keys", headers=headers, json={"name": "   "}).status_code == 422

    # Active-key cap protects against credential flooding.
    from app.security import generate_api_key

    with client.testing_session() as session:
        from app.models import User

        owner = session.scalar(select(User).where(User.email == "keys@example.com"))
        for index in range(50):
            _, prefix, digest = generate_api_key()
            session.add(ApiKey(project_id=project, created_by=owner.id, name=f"bulk-{index}", key_prefix=prefix, key_hash=digest, role="reader"))
        session.commit()
    response = client.post(f"/v1/projects/{project}/api-keys", headers=headers, json={"name": "one too many"})
    assert response.status_code == 422
    assert "50 active API keys" in response.json()["detail"]
