import hashlib
import hmac
import os
import re
import secrets
from datetime import datetime, timedelta, timezone

import jwt
from fastapi import Depends, HTTPException
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.db import get_session
from app.models import ApiKey, Membership, User

ALGORITHM = "HS256"
TOKEN_TTL_SECONDS = 900
bearer_scheme = HTTPBearer(auto_error=False)

# Project-scoped API keys: klr_<16 hex prefix>_<32 hex secret>.
API_KEY_TOKEN_PREFIX = "klr_"
API_KEY_PATTERN = re.compile(r"^klr_([0-9a-f]{16})_([0-9a-f]{32})$")
API_KEY_ROLE_READER = "reader"
API_KEY_ROLE_WRITER = "writer"
LAST_USED_TOUCH_SECONDS = 60


class Principal:
    """An authenticated caller: either a user session (JWT) or an API key acting for its creator.

    ``api_key`` is not None only for API-key callers. Effective authority is always the
    intersection of the creator's current membership role, the key's project scope, and
    the key's reader/writer cap — a key can never exceed its creator's live permissions.
    """

    __slots__ = ("user", "api_key")

    def __init__(self, user: User, api_key: ApiKey | None = None):
        self.user = user
        self.api_key = api_key

    @property
    def id(self) -> str:
        return self.user.id

    @property
    def is_api_key(self) -> bool:
        return self.api_key is not None


def _jwt_secret() -> str:
    secret = os.getenv("JWT_SECRET", "")
    if len(secret) < 32:
        raise RuntimeError("JWT_SECRET must be set to at least 32 characters")
    return secret


def hash_password(password: str) -> str:
    salt = secrets.token_bytes(16)
    digest = hashlib.pbkdf2_hmac("sha256", password.encode(), salt, 600_000)
    return f"pbkdf2_sha256$600000${salt.hex()}${digest.hex()}"


def verify_password(password: str, stored: str) -> bool:
    try:
        algorithm, iterations, salt_hex, digest_hex = stored.split("$", 3)
        if algorithm != "pbkdf2_sha256" or int(iterations) != 600_000:
            return False
        candidate = hashlib.pbkdf2_hmac("sha256", password.encode(), bytes.fromhex(salt_hex), int(iterations))
        return hmac.compare_digest(candidate.hex(), digest_hex)
    except (ValueError, TypeError):
        return False


def issue_token(user_id: str) -> str:
    now = datetime.now(timezone.utc)
    payload = {"sub": user_id, "iat": now, "exp": now + timedelta(seconds=TOKEN_TTL_SECONDS), "type": "access"}
    return jwt.encode(payload, _jwt_secret(), algorithm=ALGORITHM)


def generate_api_key() -> tuple[str, str, str]:
    """Return ``(full_token, key_prefix, sha256_digest)`` for a fresh project API key."""
    token = f"{API_KEY_TOKEN_PREFIX}{secrets.token_hex(8)}_{secrets.token_hex(16)}"
    match = API_KEY_PATTERN.match(token)
    assert match is not None  # pragma: no cover - construction is fixed-width hex
    return token, match.group(1), hash_api_key(token)


def hash_api_key(token: str) -> str:
    # The secret carries 128 random bits, so a single unsalted SHA-256 digest is
    # pre-image resistant enough while staying cheap on every request.
    return hashlib.sha256(token.encode("utf-8")).hexdigest()


def _as_utc(value: datetime | None) -> datetime | None:
    if value is None:
        return None
    return value.replace(tzinfo=timezone.utc) if value.tzinfo is None else value


def _authenticate_api_key(session: Session, token: str) -> Principal | None:
    match = API_KEY_PATTERN.match(token)
    if match is None:
        return None
    key = session.scalar(select(ApiKey).where(ApiKey.key_prefix == match.group(1)))
    if key is None:
        return None
    now = datetime.now(timezone.utc)
    if key.revoked_at is not None:
        return None
    expires_at = _as_utc(key.expires_at)
    if expires_at is not None and expires_at <= now:
        return None
    if not hmac.compare_digest(key.key_hash, hash_api_key(token)):
        return None
    user = session.get(User, key.created_by)
    if user is None:
        return None
    last_used = _as_utc(key.last_used_at)
    if last_used is None or (now - last_used).total_seconds() >= LAST_USED_TOUCH_SECONDS:
        key.last_used_at = now
        session.commit()
    return Principal(user, key)


def get_current_user(credentials: HTTPAuthorizationCredentials | None = Depends(bearer_scheme), session: Session = Depends(get_session)) -> Principal:
    credentials_error = HTTPException(
        status_code=401,
        detail="Invalid or expired authentication token",
        headers={"WWW-Authenticate": "Bearer"},
    )
    if credentials is None:
        raise credentials_error
    token = credentials.credentials
    if token.startswith(API_KEY_TOKEN_PREFIX):
        principal = _authenticate_api_key(session, token)
        if principal is None:
            raise credentials_error
        return principal
    try:
        payload = jwt.decode(
            token,
            _jwt_secret(),
            algorithms=[ALGORITHM],
            options={"require": ["exp", "iat", "sub", "type"]},
        )
        user_id = payload.get("sub")
        if payload.get("type") != "access":
            raise credentials_error
    except (jwt.InvalidTokenError, RuntimeError):
        raise credentials_error
    user = session.get(User, user_id) if isinstance(user_id, str) else None
    if user is None:
        raise credentials_error
    return Principal(user)


def require_membership(session: Session, user_id: str, organization_id: str) -> Membership:
    membership = session.scalar(select(Membership).where(
        Membership.user_id == user_id,
        Membership.organization_id == organization_id,
    ))
    if membership is None:
        raise HTTPException(status_code=404, detail="Organization or resource not found")
    return membership
