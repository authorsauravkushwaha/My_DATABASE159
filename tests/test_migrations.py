import os
import subprocess
import sys
from pathlib import Path

from alembic.autogenerate import compare_metadata
from alembic.migration import MigrationContext
from sqlalchemy import create_engine, inspect

from app import models  # noqa: F401
from app.db import Base


def test_migrations_match_models_and_round_trip(tmp_path):
    url = f'sqlite:///{tmp_path / "migration.db"}'
    env = {**os.environ, 'DATABASE_URL': url}
    root = Path(__file__).resolve().parents[1]
    def alembic(*args):
        subprocess.run([sys.executable, '-m', 'alembic', *args], cwd=root, env=env, check=True, capture_output=True, text=True)
    alembic('upgrade', 'head')
    engine = create_engine(url)
    try:
        assert 'alembic_version' in inspect(engine).get_table_names()
        assert set(inspect(engine).get_table_names()) == set(Base.metadata.tables) | {'alembic_version'}
        with engine.connect() as connection:
            assert compare_metadata(MigrationContext.configure(connection), Base.metadata) == []
        alembic('downgrade', 'base')
        assert inspect(engine).get_table_names() == ['alembic_version']
        alembic('upgrade', 'head')
    finally:
        engine.dispose()
