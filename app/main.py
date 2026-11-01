import json
import os
import re
from contextlib import asynccontextmanager
from typing import Any

from fastapi import Depends, FastAPI, HTTPException, Query, Request, Response
from fastapi.responses import StreamingResponse
from sqlalchemy import select, text, tuple_
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.compression import MAX_RECORD_BYTES, purge_engine_namespace
from app.db import Base, engine, get_session
from app.middleware import RequestBodyLimitMiddleware, SecurityHeadersMiddleware
from app.models import Collection, Membership, Organization, Project, Record, User
from app.query_engine import (
    INDEXABLE_SCALAR_TYPES,
    MAX_COLLECTION_INDEXES,
    build_index_entries,
    compact_collection_records,
    compute_project_storage_stats,
    create_secondary_index,
    drop_secondary_index,
    execute_collection_query,
    resolve_anchor,
    stream_collection_ndjson,
    sync_record_indexes,
)
from app.schemas import (
    BatchRecordCreate,
    BatchWriteResponse,
    CollectionCreate,
    CollectionResponse,
    CompactionResponse,
    DataAccessResponse,
    IndexCreateRequest,
    IndexResponse,
    LoginRequest,
    ProjectCreate,
    ProjectResponse,
    ProjectStorageResponse,
    ProjectUpdate,
    RecordCreate,
    RecordQueryRequest,
    RecordQueryResponse,
    RecordResponse,
    RecordUpdate,
    RegisterRequest,
    TokenResponse,
)
from app.security import (
    TOKEN_TTL_SECONDS,
    get_current_user,
    hash_password,
    issue_token,
    require_membership,
    verify_password,
)


@asynccontextmanager
async def lifespan(_: FastAPI):
    if len(os.getenv("JWT_SECRET", "")) < 32:
        raise RuntimeError("JWT_SECRET must be set to at least 32 random characters")
    if os.getenv("AUTO_CREATE_SCHEMA", "false").lower() == "true":
        Base.metadata.create_all(bind=engine)
    yield


app = FastAPI(
    title="Kestrel Platform API",
    description=(
        "High-density tenant-aware database platform. Supports compressed JSON collections, "
        "secondary B-tree field indexes, O(1) cursor pagination, batch ingestion, and streaming NDJSON."
    ),
    version="0.2.0",
    lifespan=lifespan,
)


app.add_middleware(SecurityHeadersMiddleware)
app.add_middleware(RequestBodyLimitMiddleware)


@app.get("/health", tags=["operations"])
def health():
    return {"status": "ok", "service": "kestrel-api"}


@app.get("/ready", tags=["operations"])
def ready(session: Session = Depends(get_session)):
    try:
        session.execute(text("SELECT 1"))
    except Exception:
        raise HTTPException(status_code=503, detail="Database is not ready")
    return {"status": "ok", "database": "ready"}


@app.post("/v1/auth/register", response_model=TokenResponse, status_code=201, tags=["authentication"])
def register(body: RegisterRequest, session: Session = Depends(get_session)):
    normalized_email = str(body.email).strip().lower()
    existing = session.scalar(select(User).where(User.email == normalized_email))
    if existing:
        raise HTTPException(status_code=409, detail="An account with this email already exists")
    user = User(email=normalized_email, password_hash=hash_password(body.password))
    organization = Organization(name=body.organization_name.strip())
    session.add_all([user, organization])
    try:
        session.flush()
        session.add(Membership(organization_id=organization.id, user_id=user.id, role="owner"))
        session.commit()
    except IntegrityError:
        session.rollback()
        raise HTTPException(status_code=409, detail="An account with this email already exists")
    return TokenResponse(
        access_token=issue_token(user.id),
        expires_in=TOKEN_TTL_SECONDS,
        user_id=user.id,
        organization_id=organization.id,
    )


@app.post("/v1/auth/login", response_model=TokenResponse, tags=["authentication"])
def login(body: LoginRequest, session: Session = Depends(get_session)):
    user = session.scalar(select(User).where(User.email == str(body.email).strip().lower()))
    if user is None or not verify_password(body.password, user.password_hash):
        raise HTTPException(status_code=401, detail="Invalid email or password", headers={"WWW-Authenticate": "Bearer"})
    organization_id = session.scalar(select(Membership.organization_id).where(Membership.user_id == user.id).limit(1))
    return TokenResponse(access_token=issue_token(user.id), expires_in=TOKEN_TTL_SECONDS, user_id=user.id, organization_id=organization_id)


@app.get("/v1/organizations/{organization_id}/projects", response_model=list[ProjectResponse], tags=["projects"])
def list_projects(organization_id: str, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    require_membership(session, user.id, organization_id)
    return list(session.scalars(select(Project).where(Project.organization_id == organization_id).order_by(Project.created_at)))


@app.post("/v1/organizations/{organization_id}/projects", response_model=ProjectResponse, status_code=201, tags=["projects"])
def create_project(organization_id: str, body: ProjectCreate, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    membership = require_membership(session, user.id, organization_id)
    if membership.role not in {"owner", "admin"}:
        raise HTTPException(status_code=403, detail="Organization admin role required")
    project = Project(organization_id=organization_id, name=body.name.strip(), slug=body.slug)
    session.add(project)
    try:
        session.commit()
    except IntegrityError:
        session.rollback()
        raise HTTPException(status_code=409, detail="A project with this slug already exists in the organization")
    session.refresh(project)
    return project


def get_authorized_project(project_id: str, user: User, session: Session) -> Project:
    project = session.get(Project, project_id)
    if project is None:
        raise HTTPException(status_code=404, detail="Project not found")
    require_membership(session, user.id, project.organization_id)
    return project


@app.get("/v1/projects/{project_id}", response_model=ProjectResponse, tags=["projects"])
def get_project(project_id: str, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    return get_authorized_project(project_id, user, session)


@app.get("/v1/projects/{project_id}/storage", response_model=ProjectStorageResponse, tags=["projects"])
def get_project_storage(project_id: str, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    return compute_project_storage_stats(session, project)


@app.patch("/v1/projects/{project_id}", response_model=ProjectResponse, tags=["projects"])
def update_project(project_id: str, body: ProjectUpdate, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    membership = require_membership(session, user.id, project.organization_id)
    if membership.role not in {"owner", "admin"}:
        raise HTTPException(status_code=403, detail="Organization admin role required")
    project.api_enabled = body.api_enabled
    session.commit()
    session.refresh(project)
    return project


@app.delete("/v1/projects/{project_id}", status_code=204, tags=["projects"])
def delete_project(project_id: str, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    membership = require_membership(session, user.id, project.organization_id)
    if membership.role not in {"owner", "admin"}:
        raise HTTPException(status_code=403, detail="Organization admin role required")
    pid = project.id
    session.delete(project)
    session.commit()
    purge_engine_namespace(pid)
    return Response(status_code=204)


SUPPORTED_TYPES = {"string", "integer", "number", "boolean", "object", "array", "null"}
FIELD_NAME = re.compile(r"^[a-zA-Z][a-zA-Z0-9_]{0,62}$")


def normalize_schema(value: dict[str, Any]) -> dict[str, Any]:
    try:
        schema_size = len(json.dumps(value, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode())
    except (TypeError, ValueError, RecursionError):
        raise HTTPException(status_code=422, detail="Collection schema must contain valid JSON values")
    if schema_size > 65_536:
        raise HTTPException(status_code=422, detail="Collection schema exceeds 64 KiB")
    if set(value) - {"type", "properties", "required", "additionalProperties", "indexes"}:
        raise HTTPException(status_code=422, detail="Unsupported collection schema keyword")
    if value.get("type", "object") != "object":
        raise HTTPException(status_code=422, detail="Collection schema type must be object")
    properties = value.get("properties", {})
    required = value.get("required", [])
    indexes = value.get("indexes", [])
    if not isinstance(properties, dict) or len(properties) > 128:
        raise HTTPException(status_code=422, detail="Schema properties must be an object with at most 128 fields")
    if not isinstance(required, list) or any(not isinstance(field, str) for field in required):
        raise HTTPException(status_code=422, detail="Schema required must be a list of field names")
    if len(set(required)) != len(required) or any(field not in properties for field in required):
        raise HTTPException(status_code=422, detail="Required fields must be unique properties in the schema")
    if not isinstance(indexes, list) or any(not isinstance(field, str) for field in indexes):
        raise HTTPException(status_code=422, detail="Schema indexes must be a list of scalar field names")
    if len(set(indexes)) != len(indexes) or len(indexes) > MAX_COLLECTION_INDEXES:
        raise HTTPException(status_code=422, detail=f"Schema indexes must be unique and at most {MAX_COLLECTION_INDEXES}")
    normalized: dict[str, Any] = {}
    for field, specification in properties.items():
        if not isinstance(field, str) or not FIELD_NAME.fullmatch(field):
            raise HTTPException(status_code=422, detail="Invalid property name")
        if not isinstance(specification, dict) or set(specification) != {"type"}:
            raise HTTPException(status_code=422, detail="Each property must declare only a supported type")
        property_type = specification["type"]
        if not isinstance(property_type, str) or property_type not in SUPPORTED_TYPES:
            raise HTTPException(status_code=422, detail=f"Unsupported property type for '{field}'")
        normalized[field] = {"type": specification["type"]}
    for idx_field in indexes:
        if idx_field not in normalized or normalized[idx_field]["type"] not in INDEXABLE_SCALAR_TYPES:
            raise HTTPException(status_code=422, detail=f"Indexed field '{idx_field}' must be a scalar schema property")
    if value.get("additionalProperties", False) is not False:
        raise HTTPException(status_code=422, detail="additionalProperties must be false")
    result: dict[str, Any] = {"type": "object", "properties": normalized, "required": required, "additionalProperties": False}
    if indexes:
        result["indexes"] = sorted(indexes)
    return result


def validate_record(collection: Collection, data: dict[str, Any]) -> None:
    if not isinstance(data, dict):
        raise HTTPException(status_code=422, detail="Record must be a JSON object")
    try:
        record_size = len(json.dumps(data, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode())
    except (TypeError, ValueError, RecursionError):
        raise HTTPException(status_code=422, detail="Record must contain valid JSON values")
    if record_size > MAX_RECORD_BYTES:
        raise HTTPException(status_code=413, detail=f"Record exceeds the {MAX_RECORD_BYTES // 1024} KiB limit")
    schema = collection.schema_json
    properties = schema["properties"]
    extra = set(data) - set(properties)
    if extra:
        raise HTTPException(status_code=422, detail=f"Unexpected fields: {', '.join(sorted(extra))}")
    missing = set(schema["required"]) - set(data)
    if missing:
        raise HTTPException(status_code=422, detail=f"Missing required fields: {', '.join(sorted(missing))}")
    for field, value in data.items():
        expected = properties[field]["type"]
        valid = {
            "string": lambda v: isinstance(v, str),
            "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
            "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
            "boolean": lambda v: isinstance(v, bool),
            "object": lambda v: isinstance(v, dict),
            "array": lambda v: isinstance(v, list),
            "null": lambda v: v is None,
        }[expected](value)
        if not valid:
            raise HTTPException(status_code=422, detail=f"Field '{field}' must be of type {expected}")


def get_collection(project: Project, name: str, session: Session) -> Collection:
    collection = session.scalar(select(Collection).where(Collection.project_id == project.id, Collection.name == name))
    if collection is None:
        raise HTTPException(status_code=404, detail="Collection not found")
    return collection


def require_project_role(project: Project, user: User, session: Session, write: bool = False) -> None:
    membership = require_membership(session, user.id, project.organization_id)
    if write and membership.role not in {"owner", "admin", "editor"}:
        raise HTTPException(status_code=403, detail="Project write role required")


def require_api_enabled(project: Project) -> None:
    if not project.api_enabled:
        raise HTTPException(status_code=403, detail="The project data API is disabled")


@app.post("/v1/projects/{project_id}/collections", response_model=CollectionResponse, status_code=201, tags=["collections"])
def create_collection(project_id: str, body: CollectionCreate, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = Collection(project_id=project.id, name=body.name, schema_json=normalize_schema(body.collection_definition))
    session.add(collection)
    try:
        session.commit()
    except IntegrityError:
        session.rollback()
        raise HTTPException(status_code=409, detail="A collection with this name already exists")
    session.refresh(collection)
    return {"id": collection.id, "project_id": collection.project_id, "name": collection.name, "schema": collection.schema_json}


@app.get("/v1/projects/{project_id}/collections", response_model=list[CollectionResponse], tags=["collections"])
def list_collections(project_id: str, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_api_enabled(project)
    collections = session.scalars(select(Collection).where(Collection.project_id == project.id).order_by(Collection.name))
    return [{"id": c.id, "project_id": c.project_id, "name": c.name, "schema": c.schema_json} for c in collections]


@app.post("/v1/projects/{project_id}/collections/{collection_name}/indexes", response_model=IndexResponse, status_code=201, tags=["collections"])
def add_collection_index(
    project_id: str,
    collection_name: str,
    body: IndexCreateRequest,
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    return create_secondary_index(session, collection, body.field)


@app.delete("/v1/projects/{project_id}/collections/{collection_name}/indexes/{field_name}", status_code=204, tags=["collections"])
def remove_collection_index(
    project_id: str,
    collection_name: str,
    field_name: str,
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    drop_secondary_index(session, collection, field_name)
    return Response(status_code=204)


@app.post("/v1/projects/{project_id}/collections/{collection_name}/compact", response_model=CompactionResponse, tags=["collections"])
def compact_collection(
    project_id: str,
    collection_name: str,
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    return compact_collection_records(session, project, collection)


@app.post("/v1/projects/{project_id}/data/{collection_name}", response_model=RecordResponse, status_code=201, tags=["data"])
def create_record(project_id: str, collection_name: str, body: RecordCreate, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    validate_record(collection, body.data)
    record = Record(project_id=project.id, collection_id=collection.id, data=body.data)
    session.add(record)
    session.flush()
    sync_record_indexes(session, collection, record.id, body.data, is_update=False)
    session.commit()
    session.refresh(record)
    return record


def _ingest_record_batch(session: Session, project: Project, collection: Collection, payloads: list[dict[str, Any]]) -> BatchWriteResponse:
    if not payloads or len(payloads) > 10_000:
        raise HTTPException(status_code=422, detail="Batch must contain between 1 and 10,000 records")
    for item in payloads:
        validate_record(collection, item)

    ids: list[str] = []
    raw_total = 0
    stored_total = 0
    chunk_size = 1000

    for start in range(0, len(payloads), chunk_size):
        sub = payloads[start : start + chunk_size]
        records_chunk: list[Record] = []
        index_chunk = []
        for item in sub:
            rec = Record(project_id=project.id, collection_id=collection.id, data=item)
            ids.append(rec.id)
            raw_total += rec.raw_size_bytes
            stored_total += rec.stored_size_bytes
            records_chunk.append(rec)
            index_chunk.extend(build_index_entries(collection, rec.id, item))
        session.add_all(records_chunk)
        session.flush()
        if index_chunk:
            session.add_all(index_chunk)
            session.flush()
        for rec in records_chunk:
            session.expunge(rec)

    session.commit()
    saved = max(0, raw_total - stored_total)
    ratio = round(raw_total / stored_total, 2) if stored_total > 0 and raw_total > 0 else 1.0
    return BatchWriteResponse(
        inserted=len(ids),
        ids=ids,
        raw_bytes=raw_total,
        stored_bytes=stored_total,
        saved_bytes=saved,
        compression_ratio=ratio,
    )


@app.post("/v1/projects/{project_id}/data/{collection_name}/batch", response_model=BatchWriteResponse, status_code=201, tags=["data"])
def batch_create_records(
    project_id: str,
    collection_name: str,
    body: BatchRecordCreate,
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    return _ingest_record_batch(session, project, collection, body.records)


@app.post("/v1/projects/{project_id}/data/{collection_name}/import", response_model=BatchWriteResponse, status_code=201, tags=["data"])
async def import_records_ndjson(
    project_id: str,
    collection_name: str,
    request: Request,
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)

    raw_body = await request.body()
    try:
        text_body = raw_body.decode("utf-8")
    except UnicodeDecodeError:
        raise HTTPException(status_code=422, detail="NDJSON import must be valid UTF-8")

    payloads: list[dict[str, Any]] = []
    for line_no, raw_line in enumerate(text_body.splitlines(), start=1):
        line = raw_line.strip()
        if not line:
            continue
        try:
            parsed = json.loads(line)
        except json.JSONDecodeError:
            raise HTTPException(status_code=422, detail=f"Invalid JSON on line {line_no}")
        if not isinstance(parsed, dict):
            raise HTTPException(status_code=422, detail=f"Line {line_no} must be a JSON object")
        # Support re-importing exported NDJSON envelopes ({"id": ..., "data": {...}}) as well as raw objects.
        if "data" in parsed and isinstance(parsed["data"], dict) and set(parsed).issubset({"id", "data", "created_at", "updated_at"}):
            payloads.append(parsed["data"])
        else:
            payloads.append(parsed)
    return _ingest_record_batch(session, project, collection, payloads)


@app.get("/v1/projects/{project_id}/data/{collection_name}/export", tags=["data"])
def export_records_ndjson(
    project_id: str,
    collection_name: str,
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    filename = f"{project.slug}-{collection.name}.ndjson"
    return StreamingResponse(
        stream_collection_ndjson(session, project.id, collection.id),
        media_type="application/x-ndjson",
        headers={"Content-Disposition": f'attachment; filename="{filename}"'},
    )


@app.post("/v1/projects/{project_id}/data/{collection_name}/query", response_model=RecordQueryResponse, tags=["data"])
def query_records(
    project_id: str,
    collection_name: str,
    body: RecordQueryRequest,
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    return execute_collection_query(
        session=session,
        project=project,
        collection=collection,
        filters=[flt.model_dump() for flt in body.filters],
        sort_dir=body.sort_dir,
        select_fields=body.select,
        limit=body.limit,
        after=body.after,
    )


@app.get("/v1/projects/{project_id}/data/{collection_name}", response_model=list[RecordResponse], tags=["data"])
def list_records(
    project_id: str,
    collection_name: str,
    response: Response,
    limit: int = Query(50, ge=1, le=100),
    offset: int = Query(0, ge=0),
    after: str | None = Query(None, max_length=128),
    user: User = Depends(get_current_user),
    session: Session = Depends(get_session),
):
    project = get_authorized_project(project_id, user, session)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    query = select(Record).where(Record.project_id == project.id, Record.collection_id == collection.id)
    if after:
        anchor = resolve_anchor(session, project.id, collection.id, after)
        query = query.where(tuple_(Record.created_at, Record.id) > anchor).order_by(Record.created_at, Record.id).limit(limit + 1)
        rows = list(session.scalars(query))
        if len(rows) > limit:
            rows = rows[:limit]
            response.headers["X-Next-Cursor"] = rows[-1].id
        return rows
    query = query.order_by(Record.created_at, Record.id).limit(limit + 1).offset(offset)
    rows = list(session.scalars(query))
    if len(rows) > limit:
        rows = rows[:limit]
        response.headers["X-Next-Cursor"] = rows[-1].id
    return rows


@app.patch("/v1/projects/{project_id}/data/{collection_name}/{record_id}", response_model=RecordResponse, tags=["data"])
def update_record(project_id: str, collection_name: str, record_id: str, body: RecordUpdate, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    record = session.scalar(select(Record).where(Record.id == record_id, Record.project_id == project.id, Record.collection_id == collection.id))
    if record is None:
        raise HTTPException(status_code=404, detail="Record not found")
    merged = {**record.data, **body.data}
    validate_record(collection, merged)
    record.data = merged
    sync_record_indexes(session, collection, record.id, merged, is_update=True)
    session.commit()
    session.refresh(record)
    return record


@app.delete("/v1/projects/{project_id}/data/{collection_name}/{record_id}", status_code=204, tags=["data"])
def delete_record(project_id: str, collection_name: str, record_id: str, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    record = session.scalar(select(Record).where(Record.id == record_id, Record.project_id == project.id, Record.collection_id == collection.id))
    if record is None:
        raise HTTPException(status_code=404, detail="Record not found")
    session.delete(record)
    session.commit()
    return Response(status_code=204)


@app.delete("/v1/projects/{project_id}/collections/{collection_name}", status_code=204, tags=["collections"])
def delete_collection(project_id: str, collection_name: str, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    collection = get_collection(project, collection_name, session)
    pid, cid = project.id, collection.id
    session.delete(collection)
    session.commit()
    purge_engine_namespace(pid, cid)
    return Response(status_code=204)


@app.get("/v1/projects/{project_id}/collections/{collection_id}/access", response_model=DataAccessResponse, tags=["internal"])
def collection_access(project_id: str, collection_id: str, write: bool = False, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    """Authorize a gateway request using the caller's token; never trust a caller-supplied tenant ID."""
    project = get_authorized_project(project_id, user, session)
    require_api_enabled(project)
    if write:
        require_project_role(project, user, session, write=True)
    collection = session.scalar(select(Collection).where(Collection.id == collection_id, Collection.project_id == project.id))
    if collection is None:
        raise HTTPException(status_code=404, detail="Collection not found")
    return DataAccessResponse(project_id=project.id, collection_id=collection.id, name=collection.name, schema=collection.schema_json)
