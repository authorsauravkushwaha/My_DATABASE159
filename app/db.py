import os
from collections.abc import Generator

from sqlalchemy import create_engine, event
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker


class Base(DeclarativeBase):
    pass


def _make_engine():
    url = os.getenv("DATABASE_URL", "sqlite:///./kestrel-dev.db")
    options: dict = {"pool_pre_ping": True}
    if url.startswith("sqlite"):
        options["connect_args"] = {"check_same_thread": False, "timeout": 15}
    else:
        options["pool_size"] = int(os.getenv("DB_POOL_SIZE", "20"))
        options["max_overflow"] = int(os.getenv("DB_MAX_OVERFLOW", "40"))
        options["pool_recycle"] = 1800
    return create_engine(url, **options)


engine = _make_engine()
if engine.dialect.name == "sqlite":
    @event.listens_for(engine, "connect")
    def _configure_sqlite(connection, _record):
        cursor = connection.cursor()
        try:
            cursor.execute("PRAGMA foreign_keys=ON")
            cursor.execute("PRAGMA temp_store=MEMORY")
            cursor.execute("PRAGMA cache_size=-65536")
            cursor.execute("PRAGMA synchronous=NORMAL")
            cursor.execute("PRAGMA journal_mode=WAL")
        except Exception:
            pass
        finally:
            cursor.close()


SessionLocal = sessionmaker(bind=engine, autoflush=False, expire_on_commit=False)


def get_session() -> Generator[Session, None, None]:
    session = SessionLocal()
    try:
        yield session
    finally:
        session.close()
