"""project-scoped API keys

Revision ID: 0003
Revises: 0002
"""
import sqlalchemy as sa

from alembic import op

revision = '0003'
down_revision = '0002'
branch_labels = None
depends_on = None


def upgrade():
    op.create_table(
        'api_keys',
        sa.Column('id', sa.String(length=36), nullable=False),
        sa.Column('project_id', sa.String(length=36), nullable=False),
        sa.Column('created_by', sa.String(length=36), nullable=False),
        sa.Column('name', sa.String(length=80), nullable=False),
        sa.Column('key_prefix', sa.String(length=16), nullable=False),
        sa.Column('key_hash', sa.String(length=64), nullable=False),
        sa.Column('role', sa.String(length=10), nullable=False),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('expires_at', sa.DateTime(timezone=True), nullable=True),
        sa.Column('last_used_at', sa.DateTime(timezone=True), nullable=True),
        sa.Column('revoked_at', sa.DateTime(timezone=True), nullable=True),
        sa.ForeignKeyConstraint(['project_id'], ['projects.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['created_by'], ['users.id'], ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id'),
        sa.UniqueConstraint('key_prefix', name='uq_api_key_prefix'),
    )
    with op.batch_alter_table('api_keys', schema=None) as batch_op:
        batch_op.create_index(batch_op.f('ix_api_keys_project_id'), ['project_id'], unique=False)
        batch_op.create_index(batch_op.f('ix_api_keys_created_by'), ['created_by'], unique=False)
        batch_op.create_index('ix_api_keys_project_revoked', ['project_id', 'revoked_at'], unique=False)


def downgrade():
    with op.batch_alter_table('api_keys', schema=None) as batch_op:
        batch_op.drop_index('ix_api_keys_project_revoked')
        batch_op.drop_index(batch_op.f('ix_api_keys_created_by'))
        batch_op.drop_index(batch_op.f('ix_api_keys_project_id'))

    op.drop_table('api_keys')
