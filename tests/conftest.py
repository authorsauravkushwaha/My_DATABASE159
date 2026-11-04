import os

os.environ.setdefault("JWT_SECRET", "test-only-secret-that-is-at-least-32-bytes")
# Keep the full suite far away from the default per-minute rate windows; the
# limiter's own behavior is covered by focused tests with explicit small limits.
os.environ.setdefault("RATE_LIMIT_MAX_REQUESTS", "1000000")
os.environ.setdefault("RATE_LIMIT_AUTH_MAX_REQUESTS", "1000000")
os.environ.pop("METRICS_TOKEN", None)

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, event
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from app.db import Base, get_session
from app.main import app


@pytest.fixture()
def client():
    engine = create_engine("sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool)

    @event.listens_for(engine, "connect")
    def enable_foreign_keys(connection, _record):
        cursor = connection.cursor()
        cursor.execute("PRAGMA foreign_keys=ON")
        cursor.close()

    Base.metadata.create_all(engine)
    testing_session = sessionmaker(bind=engine, autoflush=False, expire_on_commit=False)

    def override_session():
        session = testing_session()
        try:
            yield session
        finally:
            session.close()

    app.dependency_overrides[get_session] = override_session
    with TestClient(app) as test_client:
        test_client.testing_session = testing_session
        yield test_client
    app.dependency_overrides.clear()
    Base.metadata.drop_all(engine)
    engine.dispose()
