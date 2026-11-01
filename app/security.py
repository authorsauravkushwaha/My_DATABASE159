import hashlib
import hmac
import os
import secrets
from datetime import datetime, timedelta, timezone

import jwt
from fastapi import Depends, HTTPException
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.db import get_session
from app.models import Membership, User

ALGORITHM = "HS256"
TOKEN_TTL_SECONDS = 900
bearer_scheme = HTTPBearer(auto_error=False)


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
    payload = {"sub": user_id, "iat": now, "exp": now + timedelta(seconds=TOKEN_TTL_SECONDS)}
    return jwt.encode(payload, _jwt_secret(), algorithm=ALGORITHM)


def get_current_user(credentials: HTTPAuthorizationCredentials | None = Depends(bearer_scheme), session: Session = Depends(get_session)) -> User:
    credentials_error = HTTPException(
        status_code=401,
        detail="Invalid or expired authentication token",
        headers={"WWW-Authenticate": "Bearer"},
    )
    if credentials is None:
        raise credentials_error
    try:
        payload = jwt.decode(credentials.credentials, _jwt_secret(), algorithms=[ALGORITHM], options={"require": ["exp", "iat", "sub"]})
        user_id = payload.get("sub")
    except (jwt.InvalidTokenError, RuntimeError):
        raise credentials_error
    user = session.get(User, user_id) if isinstance(user_id, str) else None
    if user is None:
        raise credentials_error
    return user


def require_membership(session: Session, user_id: str, organization_id: str) -> Membership:
    membership = session.scalar(select(Membership).where(
        Membership.user_id == user_id,
        Membership.organization_id == organization_id,
    ))
    if membership is None:
        raise HTTPException(status_code=404, detail="Organization or resource not found")
    return membership
