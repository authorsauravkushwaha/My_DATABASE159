from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, EmailStr, Field, field_validator

FilterOperator = Literal["eq", "neq", "gt", "gte", "lt", "lte", "in", "contains", "prefix"]


class RegisterRequest(BaseModel):
    email: EmailStr
    password: str = Field(min_length=12, max_length=128)
    organization_name: str = Field(min_length=2, max_length=120)

    @field_validator("organization_name")
    @classmethod
    def nonblank_organization(cls, value: str) -> str:
        value = value.strip()
        if len(value) < 2:
            raise ValueError("Organization name must contain at least two non-whitespace characters")
        return value

    @field_validator("password")
    @classmethod
    def password_not_common_placeholder(cls, value: str) -> str:
        if value.lower() in {"password1234", "changeme12345", "123456789012"}:
            raise ValueError("Choose a less predictable password")
        return value


class LoginRequest(BaseModel):
    email: EmailStr
    password: str = Field(min_length=1, max_length=128)


class TokenResponse(BaseModel):
    access_token: str
    token_type: str = "bearer"
    expires_in: int
    user_id: str
    organization_id: str | None = None


class ProjectCreate(BaseModel):
    name: str = Field(min_length=2, max_length=120)
    slug: str = Field(min_length=2, max_length=80, pattern=r"^[a-z0-9]+(?:-[a-z0-9]+)*$")

    @field_validator("name")
    @classmethod
    def nonblank_name(cls, value: str) -> str:
        value = value.strip()
        if len(value) < 2:
            raise ValueError("Project name must contain at least two non-whitespace characters")
        return value


class ProjectUpdate(BaseModel):
    api_enabled: bool


class ProjectResponse(BaseModel):
    id: str
    organization_id: str
    name: str
    slug: str
    api_enabled: bool
    created_at: Any

    model_config = ConfigDict(from_attributes=True)


class CollectionCreate(BaseModel):
    name: str = Field(min_length=1, max_length=63, pattern=r"^[a-z][a-z0-9_]*$")
    collection_definition: dict[str, Any] = Field(default_factory=dict, max_length=16, alias="schema")

    model_config = ConfigDict(populate_by_name=True)


class CollectionResponse(BaseModel):
    id: str
    project_id: str
    name: str
    collection_definition: dict[str, Any] = Field(alias="schema")

    model_config = ConfigDict(populate_by_name=True)


class RecordCreate(BaseModel):
    data: dict[str, Any]


class RecordUpdate(BaseModel):
    data: dict[str, Any]


class RecordResponse(BaseModel):
    id: str
    data: dict[str, Any]
    created_at: Any
    updated_at: Any

    model_config = ConfigDict(from_attributes=True)


class BatchRecordCreate(BaseModel):
    records: list[dict[str, Any]] = Field(min_length=1, max_length=5000)


class BatchWriteResponse(BaseModel):
    inserted: int
    ids: list[str]
    raw_bytes: int
    stored_bytes: int
    saved_bytes: int
    compression_ratio: float


class QueryFilter(BaseModel):
    field: str = Field(min_length=1, max_length=63)
    op: FilterOperator = "eq"
    value: Any = None


class RecordQueryRequest(BaseModel):
    filters: list[QueryFilter] = Field(default_factory=list, max_length=16)
    sort_dir: Literal["asc", "desc"] = "asc"
    select: list[str] | None = Field(default=None, max_length=128)
    limit: int = Field(default=100, ge=1, le=1000)
    after: str | None = Field(default=None, max_length=128)


class RecordQueryResponse(BaseModel):
    records: list[RecordResponse]
    next_cursor: str | None = None
    returned: int
    scanned: int
    index_used: bool
    indexed_fields_used: list[str]


class IndexCreateRequest(BaseModel):
    field: str = Field(min_length=1, max_length=63)


class IndexResponse(BaseModel):
    collection: str
    field: str
    indexed_entries: int
    indexes: list[str]


class CompactionResponse(BaseModel):
    collection: str
    total_records: int
    recompressed_records: int
    raw_bytes: int
    stored_bytes: int
    saved_bytes: int
    compression_ratio: float


class CollectionStorageStats(BaseModel):
    id: str
    name: str
    records: int
    compressed_records: int
    raw_bytes: int
    stored_bytes: int
    saved_bytes: int
    compression_ratio: float
    indexes: list[str]


class ProjectStorageResponse(BaseModel):
    project_id: str
    total_collections: int
    total_records: int
    compressed_records: int
    raw_bytes: int
    stored_bytes: int
    saved_bytes: int
    compression_ratio: float
    indexed_fields_count: int
    virtual_shards: int
    active_shards: int
    collections: list[CollectionStorageStats]
    engine: dict[str, Any] | None = None


class DataAccessResponse(BaseModel):
    project_id: str
    collection_id: str
    name: str
    collection_definition: dict[str, Any] = Field(alias="schema")

    model_config = ConfigDict(populate_by_name=True)


class ErrorResponse(BaseModel):
    detail: str


class ApiKeyCreate(BaseModel):
    name: str = Field(min_length=1, max_length=80)
    role: Literal["reader", "writer"] = "reader"
    expires_in_days: int | None = Field(default=None, ge=1, le=3650)

    @field_validator("name")
    @classmethod
    def nonblank_name(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError("API key name must not be blank")
        return value


class ApiKeyResponse(BaseModel):
    id: str
    project_id: str
    name: str
    role: str
    key_prefix: str
    created_by: str
    created_at: Any
    expires_at: Any | None = None
    last_used_at: Any | None = None
    revoked_at: Any | None = None

    model_config = ConfigDict(from_attributes=True)


class ApiKeyCreatedResponse(ApiKeyResponse):
    """Created key metadata plus the one-time full secret. ``api_key`` is never retrievable again."""

    api_key: str
