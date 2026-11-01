"""high capacity compressed storage and secondary field indexes

Revision ID: 0002
Revises: 0001
"""
import sqlalchemy as sa

from alembic import op

revision = '0002'
down_revision = '0001'
branch_labels = None
depends_on = None


def upgrade():
    with op.batch_alter_table('records', schema=None) as batch_op:
        batch_op.alter_column('data', existing_type=sa.JSON(), nullable=True)
        batch_op.add_column(sa.Column('compressed_data', sa.LargeBinary(), nullable=True))
        batch_op.add_column(sa.Column('raw_size_bytes', sa.Integer(), nullable=False, server_default='0'))
        batch_op.add_column(sa.Column('stored_size_bytes', sa.Integer(), nullable=False, server_default='0'))
        batch_op.add_column(sa.Column('shard_key', sa.Integer(), nullable=False, server_default='0'))
        batch_op.create_index('ix_records_collection_cursor', ['project_id', 'collection_id', 'created_at', 'id'], unique=False)
        batch_op.create_index('ix_records_collection_shard', ['collection_id', 'shard_key'], unique=False)

    op.create_table(
        'record_field_indexes',
        sa.Column('id', sa.Integer(), autoincrement=True, nullable=False),
        sa.Column('collection_id', sa.String(length=36), nullable=False),
        sa.Column('record_id', sa.String(length=36), nullable=False),
        sa.Column('field_name', sa.String(length=63), nullable=False),
        sa.Column('value_type', sa.String(length=16), nullable=False),
        sa.Column('str_value', sa.String(length=512), nullable=True),
        sa.Column('num_value', sa.Float(), nullable=True),
        sa.Column('bool_value', sa.Boolean(), nullable=True),
        sa.ForeignKeyConstraint(['collection_id'], ['collections.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['record_id'], ['records.id'], ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id'),
        sa.UniqueConstraint('record_id', 'field_name', name='uq_record_field_index'),
    )
    with op.batch_alter_table('record_field_indexes', schema=None) as batch_op:
        batch_op.create_index(batch_op.f('ix_record_field_indexes_collection_id'), ['collection_id'], unique=False)
        batch_op.create_index(batch_op.f('ix_record_field_indexes_record_id'), ['record_id'], unique=False)
        batch_op.create_index('ix_rfi_coll_field_str', ['collection_id', 'field_name', 'str_value', 'record_id'], unique=False)
        batch_op.create_index('ix_rfi_coll_field_num', ['collection_id', 'field_name', 'num_value', 'record_id'], unique=False)
        batch_op.create_index('ix_rfi_coll_field_bool', ['collection_id', 'field_name', 'bool_value', 'record_id'], unique=False)


def downgrade():
    with op.batch_alter_table('record_field_indexes', schema=None) as batch_op:
        batch_op.drop_index('ix_rfi_coll_field_bool')
        batch_op.drop_index('ix_rfi_coll_field_num')
        batch_op.drop_index('ix_rfi_coll_field_str')
        batch_op.drop_index(batch_op.f('ix_record_field_indexes_record_id'))
        batch_op.drop_index(batch_op.f('ix_record_field_indexes_collection_id'))

    op.drop_table('record_field_indexes')

    # Backfill inline JSON empty object before restoring NOT NULL constraint if any compressed rows remain.
    op.execute(sa.text("UPDATE records SET data = '{}' WHERE data IS NULL"))

    with op.batch_alter_table('records', schema=None) as batch_op:
        batch_op.drop_index('ix_records_collection_shard')
        batch_op.drop_index('ix_records_collection_cursor')
        batch_op.drop_column('shard_key')
        batch_op.drop_column('stored_size_bytes')
        batch_op.drop_column('raw_size_bytes')
        batch_op.drop_column('compressed_data')
        batch_op.alter_column('data', existing_type=sa.JSON(), nullable=False)
