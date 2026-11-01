import json
import os
import re
from contextlib import asynccontextmanager
from typing import Any

from fastapi import Depends, FastAPI, HTTPException, Query, Response
from sqlalchemy import select, text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.db import Base, engine, get_session
from app.middleware import RequestBodyLimitMiddleware
from app.models import Collection, Membership, Organization, Project, Record, User
from app.schemas import (
    CollectionCreate,
    CollectionResponse,
    DataAccessResponse,
    LoginRequest,
    ProjectCreate,
    ProjectResponse,
    ProjectUpdate,
    RecordCreate,
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
    description="Tenant-aware database platform MVP. This API manages projects and validated JSON collections; it does not execute client SQL.",
    version="0.2.0",
    lifespan=lifespan,
)


app.add_middleware(RequestBodyLimitMiddleware)


@app.middleware("http")
async def security_headers(request, call_next):
    response = await call_next(request)
    response.headers["X-Content-Type-Options"] = "nosniff"
    response.headers["X-Frame-Options"] = "DENY"
    response.headers["Referrer-Policy"] = "no-referrer"
    response.headers["Cache-Control"] = "no-store"
    if os.getenv("APP_ENV") == "production":
        response.headers["Strict-Transport-Security"] = "max-age=31536000; includeSubDomains"
    return response


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
        access_token=issue_token(user.id), expires_in=TOKEN_TTL_SECONDS,
        user_id=user.id, organization_id=organization.id,
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
    session.delete(project)
    session.commit()
    return Response(status_code=204)


SUPPORTED_TYPES = {"string", "integer", "number", "boolean", "object", "array", "null"}
FIELD_NAME = re.compile(r"^[a-zA-Z][a-zA-Z0-9_]{0,62}$")


def normalize_schema(value: dict[str, Any]) -> dict[str, Any]:
    try:
        schema_size = len(json.dumps(value, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode())
    except (TypeError, ValueError, RecursionError):
        raise HTTPException(status_code=422, detail="Collection schema must contain valid JSON values")
    if schema_size > 16_384:
        raise HTTPException(status_code=422, detail="Collection schema exceeds 16 KiB")
    if set(value) - {"type", "properties", "required", "additionalProperties"}:
        raise HTTPException(status_code=422, detail="Unsupported collection schema keyword")
    if value.get("type", "object") != "object":
        raise HTTPException(status_code=422, detail="Collection schema type must be object")
    properties = value.get("properties", {})
    required = value.get("required", [])
    if not isinstance(properties, dict) or len(properties) > 32:
        raise HTTPException(status_code=422, detail="Schema properties must be an object with at most 32 fields")
    if not isinstance(required, list) or any(not isinstance(field, str) for field in required):
        raise HTTPException(status_code=422, detail="Schema required must be a list of field names")
    if len(set(required)) != len(required) or any(field not in properties for field in required):
        raise HTTPException(status_code=422, detail="Required fields must be unique properties in the schema")
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
    if value.get("additionalProperties", False) is not False:
        raise HTTPException(status_code=422, detail="additionalProperties must be false")
    return {"type": "object", "properties": normalized, "required": required, "additionalProperties": False}


def validate_record(collection: Collection, data: dict[str, Any]) -> None:
    try:
        record_size = len(json.dumps(data, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode())
    except (TypeError, ValueError, RecursionError):
        raise HTTPException(status_code=422, detail="Record must contain valid JSON values")
    if record_size > 65_536:
        raise HTTPException(status_code=413, detail="Record exceeds the 64 KiB MVP limit")
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


@app.post("/v1/projects/{project_id}/data/{collection_name}", response_model=RecordResponse, status_code=201, tags=["data"])
def create_record(project_id: str, collection_name: str, body: RecordCreate, user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_project_role(project, user, session, write=True)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    validate_record(collection, body.data)
    record = Record(project_id=project.id, collection_id=collection.id, data=body.data)
    session.add(record)
    session.commit()
    session.refresh(record)
    return record


@app.get("/v1/projects/{project_id}/data/{collection_name}", response_model=list[RecordResponse], tags=["data"])
def list_records(project_id: str, collection_name: str, limit: int = Query(50, ge=1, le=100), offset: int = Query(0, ge=0), user: User = Depends(get_current_user), session: Session = Depends(get_session)):
    project = get_authorized_project(project_id, user, session)
    require_api_enabled(project)
    collection = get_collection(project, collection_name, session)
    query = select(Record).where(Record.project_id == project.id, Record.collection_id == collection.id).order_by(Record.created_at, Record.id).limit(limit).offset(offset)
    return list(session.scalars(query))


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
    session.delete(collection)
    session.commit()
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
