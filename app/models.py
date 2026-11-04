import uuid
from datetime import datetime, timezone
from typing import Any

from sqlalchemy import (
    JSON,
    Boolean,
    DateTime,
    Float,
    ForeignKey,
    Index,
    Integer,
    LargeBinary,
    String,
    UniqueConstraint,
)
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.compression import compute_shard_key, decode_record_payload, encode_record_payload
from app.db import Base


def new_id() -> str:
    return str(uuid.uuid4())


def utcnow() -> datetime:
    return datetime.now(timezone.utc)


class User(Base):
    __tablename__ = "users"
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=new_id)
    email: Mapped[str] = mapped_column(String(320), unique=True, index=True)
    password_hash: Mapped[str] = mapped_column(String(256))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class Organization(Base):
    __tablename__ = "organizations"
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=new_id)
    name: Mapped[str] = mapped_column(String(120))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class Membership(Base):
    __tablename__ = "memberships"
    __table_args__ = (UniqueConstraint("organization_id", "user_id", name="uq_membership_org_user"),)
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=new_id)
    organization_id: Mapped[str] = mapped_column(ForeignKey("organizations.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    role: Mapped[str] = mapped_column(String(20), default="owner")


class Project(Base):
    __tablename__ = "projects"
    __table_args__ = (UniqueConstraint("organization_id", "slug", name="uq_project_org_slug"),)
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=new_id)
    organization_id: Mapped[str] = mapped_column(ForeignKey("organizations.id", ondelete="CASCADE"), index=True)
    name: Mapped[str] = mapped_column(String(120))
    slug: Mapped[str] = mapped_column(String(80))
    api_enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    collections: Mapped[list["Collection"]] = relationship(cascade="all, delete-orphan", passive_deletes=True)


class Collection(Base):
    __tablename__ = "collections"
    __table_args__ = (UniqueConstraint("project_id", "name", name="uq_collection_project_name"),)
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=new_id)
    project_id: Mapped[str] = mapped_column(ForeignKey("projects.id", ondelete="CASCADE"), index=True)
    name: Mapped[str] = mapped_column(String(63))
    schema_json: Mapped[dict] = mapped_column(JSON)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class Record(Base):
    __tablename__ = "records"
    __table_args__ = (
        UniqueConstraint("collection_id", "id", name="uq_record_collection_id"),
        Index("ix_records_collection_cursor", "project_id", "collection_id", "created_at", "id"),
        Index("ix_records_collection_shard", "collection_id", "shard_key"),
    )
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=new_id)
    project_id: Mapped[str] = mapped_column(ForeignKey("projects.id", ondelete="CASCADE"), index=True)
    collection_id: Mapped[str] = mapped_column(ForeignKey("collections.id", ondelete="CASCADE"), index=True)
    inline_data: Mapped[dict | None] = mapped_column("data", JSON, nullable=True)
    compressed_data: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True)
    raw_size_bytes: Mapped[int] = mapped_column(Integer, default=0)
    stored_size_bytes: Mapped[int] = mapped_column(Integer, default=0)
    shard_key: Mapped[int] = mapped_column(Integer, default=0)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, onupdate=utcnow)

    def __init__(self, **kwargs: Any):
        has_data = "data" in kwargs
        payload = kwargs.pop("data", None)
        if "id" not in kwargs or kwargs["id"] is None:
            kwargs["id"] = new_id()
        if "shard_key" not in kwargs:
            kwargs["shard_key"] = compute_shard_key(kwargs["id"])
        super().__init__(**kwargs)
        if has_data and payload is not None:
            self.data = payload

    @property
    def data(self) -> dict[str, Any]:
        return decode_record_payload(self.inline_data, self.compressed_data)

    @data.setter
    def data(self, value: dict[str, Any]) -> None:
        inline, compressed, raw_len, stored_len = encode_record_payload(value)
        self.inline_data = inline
        self.compressed_data = compressed
        self.raw_size_bytes = raw_len
        self.stored_size_bytes = stored_len
        if self.id:
            self.shard_key = compute_shard_key(self.id)


class ApiKey(Base):
    """Project-scoped API key. Only a SHA-256 digest of the secret is stored."""

    __tablename__ = "api_keys"
    __table_args__ = (
        UniqueConstraint("key_prefix", name="uq_api_key_prefix"),
        Index("ix_api_keys_project_revoked", "project_id", "revoked_at"),
    )
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=new_id)
    project_id: Mapped[str] = mapped_column(ForeignKey("projects.id", ondelete="CASCADE"), index=True)
    created_by: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    name: Mapped[str] = mapped_column(String(80))
    key_prefix: Mapped[str] = mapped_column(String(16))
    key_hash: Mapped[str] = mapped_column(String(64))
    role: Mapped[str] = mapped_column(String(10), default="reader")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    expires_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    last_used_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    revoked_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)


class RecordFieldIndex(Base):
    __tablename__ = "record_field_indexes"
    __table_args__ = (
        UniqueConstraint("record_id", "field_name", name="uq_record_field_index"),
        Index("ix_rfi_coll_field_str", "collection_id", "field_name", "str_value", "record_id"),
        Index("ix_rfi_coll_field_num", "collection_id", "field_name", "num_value", "record_id"),
        Index("ix_rfi_coll_field_bool", "collection_id", "field_name", "bool_value", "record_id"),
    )
    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    collection_id: Mapped[str] = mapped_column(ForeignKey("collections.id", ondelete="CASCADE"), index=True)
    record_id: Mapped[str] = mapped_column(ForeignKey("records.id", ondelete="CASCADE"), index=True)
    field_name: Mapped[str] = mapped_column(String(63))
    value_type: Mapped[str] = mapped_column(String(16))
    str_value: Mapped[str | None] = mapped_column(String(512), nullable=True)
    num_value: Mapped[float | None] = mapped_column(Float, nullable=True)
    bool_value: Mapped[bool | None] = mapped_column(Boolean, nullable=True)
