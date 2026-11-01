from typing import Any

from pydantic import BaseModel, ConfigDict, EmailStr, Field, field_validator


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
    collection_definition: dict[str, Any] = Field(default_factory=dict, max_length=32, alias="schema")

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


class ErrorResponse(BaseModel):
    detail: str
