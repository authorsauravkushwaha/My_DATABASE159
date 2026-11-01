"""Query planner, secondary B-tree index manager, batch ingestion, and streaming NDJSON."""
from __future__ import annotations

import json
from collections.abc import Iterator
from typing import Any

from fastapi import HTTPException
from sqlalchemy import delete, func, select, tuple_
from sqlalchemy.orm import Session

from app.compression import SHARD_BUCKETS, encode_record_payload, get_engine_stats
from app.models import Collection, Project, Record, RecordFieldIndex

INDEXABLE_SCALAR_TYPES = {"string", "integer", "number", "boolean"}
MAX_COLLECTION_INDEXES = 16
MAX_STR_INDEX_LEN = 512
STREAM_CHUNK_SIZE = 500
MAX_SCAN_PER_QUERY = 100_000


def get_indexed_fields(collection: Collection) -> list[str]:
    raw = collection.schema_json.get("indexes", [])
    if not isinstance(raw, list):
        return []
    return [f for f in raw if isinstance(f, str)]


def build_index_entries(collection: Collection, record_id: str, data: dict[str, Any]) -> list[RecordFieldIndex]:
    indexed = get_indexed_fields(collection)
    if not indexed:
        return []
    properties = collection.schema_json.get("properties", {})
    rows: list[RecordFieldIndex] = []
    for field in indexed:
        if field not in data or field not in properties:
            continue
        val = data[field]
        prop_type = properties[field]["type"]
        if val is None:
            rows.append(
                RecordFieldIndex(
                    collection_id=collection.id,
                    record_id=record_id,
                    field_name=field,
                    value_type="null",
                )
            )
        elif prop_type == "string" and isinstance(val, str):
            rows.append(
                RecordFieldIndex(
                    collection_id=collection.id,
                    record_id=record_id,
                    field_name=field,
                    value_type="string",
                    str_value=val[:MAX_STR_INDEX_LEN],
                )
            )
        elif prop_type in {"integer", "number"} and isinstance(val, (int, float)) and not isinstance(val, bool):
            rows.append(
                RecordFieldIndex(
                    collection_id=collection.id,
                    record_id=record_id,
                    field_name=field,
                    value_type="number",
                    num_value=float(val),
                )
            )
        elif prop_type == "boolean" and isinstance(val, bool):
            rows.append(
                RecordFieldIndex(
                    collection_id=collection.id,
                    record_id=record_id,
                    field_name=field,
                    value_type="boolean",
                    bool_value=val,
                )
            )
    return rows


def sync_record_indexes(session: Session, collection: Collection, record_id: str, data: dict[str, Any], is_update: bool) -> None:
    if is_update:
        session.execute(delete(RecordFieldIndex).where(RecordFieldIndex.record_id == record_id))
    entries = build_index_entries(collection, record_id, data)
    if entries:
        session.add_all(entries)


def create_secondary_index(session: Session, collection: Collection, field_name: str) -> dict[str, Any]:
    properties = collection.schema_json.get("properties", {})
    if field_name not in properties:
        raise HTTPException(status_code=422, detail=f"Field '{field_name}' does not exist in collection schema")
    prop_type = properties[field_name]["type"]
    if prop_type not in INDEXABLE_SCALAR_TYPES:
        raise HTTPException(status_code=422, detail=f"Only scalar fields ({', '.join(sorted(INDEXABLE_SCALAR_TYPES))}) can be indexed")

    current = get_indexed_fields(collection)
    if field_name in current:
        raise HTTPException(status_code=409, detail=f"Index on '{field_name}' already exists")
    if len(current) >= MAX_COLLECTION_INDEXES:
        raise HTTPException(status_code=422, detail=f"At most {MAX_COLLECTION_INDEXES} secondary indexes are allowed per collection")

    updated_indexes = sorted(set(current) | {field_name})
    updated_schema = {**collection.schema_json, "indexes": updated_indexes}
    collection.schema_json = updated_schema

    # Backfill existing records in bounded cursor chunks.
    indexed_rows = 0
    cursor_anchor: tuple[Any, str] | None = None
    temp_collection = Collection(id=collection.id, project_id=collection.project_id, name=collection.name, schema_json={"properties": properties, "indexes": [field_name]})
    while True:
        stmt = select(Record).where(Record.collection_id == collection.id)
        if cursor_anchor is not None:
            stmt = stmt.where(tuple_(Record.created_at, Record.id) > cursor_anchor)
        stmt = stmt.order_by(Record.created_at, Record.id).limit(STREAM_CHUNK_SIZE)
        chunk = list(session.scalars(stmt))
        if not chunk:
            break
        batch_entries: list[RecordFieldIndex] = []
        for rec in chunk:
            batch_entries.extend(build_index_entries(temp_collection, rec.id, rec.data))
        if batch_entries:
            session.add_all(batch_entries)
            indexed_rows += len(batch_entries)
        cursor_anchor = (chunk[-1].created_at, chunk[-1].id)
        session.flush()

    session.commit()
    return {"collection": collection.name, "field": field_name, "indexed_entries": indexed_rows, "indexes": updated_indexes}


def drop_secondary_index(session: Session, collection: Collection, field_name: str) -> None:
    current = get_indexed_fields(collection)
    if field_name not in current:
        raise HTTPException(status_code=404, detail=f"Index on '{field_name}' not found")
    remaining = [f for f in current if f != field_name]
    updated_schema = dict(collection.schema_json)
    if remaining:
        updated_schema["indexes"] = remaining
    else:
        updated_schema.pop("indexes", None)
    collection.schema_json = updated_schema
    session.execute(
        delete(RecordFieldIndex).where(
            RecordFieldIndex.collection_id == collection.id,
            RecordFieldIndex.field_name == field_name,
        )
    )
    session.commit()


def _next_prefix(prefix: str) -> str | None:
    if not prefix:
        return None
    for idx in range(len(prefix) - 1, -1, -1):
        code = ord(prefix[idx])
        if code < 0x10FFFF:
            return prefix[:idx] + chr(code + 1)
    return None


def _build_index_subquery(collection_id: str, prop_type: str, field: str, op: str, val: Any):
    base = select(RecordFieldIndex.record_id).where(
        RecordFieldIndex.collection_id == collection_id,
        RecordFieldIndex.field_name == field,
    )
    if prop_type in {"integer", "number"} and isinstance(val, (int, float)) and not isinstance(val, bool):
        fval = float(val)
        col = RecordFieldIndex.num_value
        if op == "eq":
            return base.where(RecordFieldIndex.value_type == "number", col == fval), False
        if op == "gt":
            return base.where(RecordFieldIndex.value_type == "number", col > fval), False
        if op == "gte":
            return base.where(RecordFieldIndex.value_type == "number", col >= fval), False
        if op == "lt":
            return base.where(RecordFieldIndex.value_type == "number", col < fval), False
        if op == "lte":
            return base.where(RecordFieldIndex.value_type == "number", col <= fval), False
    elif prop_type in {"integer", "number"} and op == "in" and isinstance(val, list):
        nums = [float(x) for x in val if isinstance(x, (int, float)) and not isinstance(x, bool)]
        if nums and len(nums) == len(val):
            return base.where(RecordFieldIndex.value_type == "number", RecordFieldIndex.num_value.in_(nums)), False
    elif prop_type == "boolean" and isinstance(val, bool) and op == "eq":
        return base.where(RecordFieldIndex.value_type == "boolean", RecordFieldIndex.bool_value == val), False
    elif prop_type == "string" and isinstance(val, str) and len(val) <= MAX_STR_INDEX_LEN:
        scol = RecordFieldIndex.str_value
        needs_post_check = len(val) == MAX_STR_INDEX_LEN
        if op == "eq":
            return base.where(RecordFieldIndex.value_type == "string", scol == val), needs_post_check
        if op == "gt":
            return base.where(RecordFieldIndex.value_type == "string", scol > val), True
        if op == "gte":
            return base.where(RecordFieldIndex.value_type == "string", scol >= val), needs_post_check
        if op == "lt":
            return base.where(RecordFieldIndex.value_type == "string", scol < val), True
        if op == "lte":
            return base.where(RecordFieldIndex.value_type == "string", scol <= val), True
        if op == "prefix":
            if val == "":
                return base.where(RecordFieldIndex.value_type == "string"), False
            upper = _next_prefix(val)
            if upper is not None:
                return base.where(RecordFieldIndex.value_type == "string", scol >= val, scol < upper), needs_post_check
    elif prop_type == "string" and op == "in" and isinstance(val, list):
        strs = [x for x in val if isinstance(x, str) and len(x) < MAX_STR_INDEX_LEN]
        if strs and len(strs) == len(val):
            return base.where(RecordFieldIndex.value_type == "string", RecordFieldIndex.str_value.in_(strs)), False
    return None, True


def matches_predicate(record_id: str, data: dict[str, Any], field: str, op: str, expected: Any) -> bool:
    if field == "id":
        actual: Any = record_id
        exists = True
    else:
        exists = field in data
        actual = data.get(field)

    if not exists:
        return False
    if op == "eq":
        return actual == expected and type(actual) is not bool if isinstance(expected, (int, float)) and not isinstance(expected, bool) else actual == expected
    if op == "neq":
        return actual != expected
    if op == "in":
        return isinstance(expected, list) and actual in expected
    if op == "contains":
        if isinstance(actual, str) and isinstance(expected, str):
            return expected.lower() in actual.lower()
        if isinstance(actual, list):
            return expected in actual
        return False
    if op == "prefix":
        return isinstance(actual, str) and isinstance(expected, str) and actual.startswith(expected)

    if isinstance(actual, bool) or isinstance(expected, bool):
        return False
    if isinstance(actual, (int, float)) and isinstance(expected, (int, float)):
        if op == "gt":
            return actual > expected
        if op == "gte":
            return actual >= expected
        if op == "lt":
            return actual < expected
        if op == "lte":
            return actual <= expected
    if isinstance(actual, str) and isinstance(expected, str):
        if op == "gt":
            return actual > expected
        if op == "gte":
            return actual >= expected
        if op == "lt":
            return actual < expected
        if op == "lte":
            return actual <= expected
    return False


def resolve_anchor(session: Session, project_id: str, collection_id: str, after_id: str) -> tuple[Any, str]:
    anchor = session.scalar(
        select(Record).where(
            Record.id == after_id,
            Record.project_id == project_id,
            Record.collection_id == collection_id,
        )
    )
    if anchor is None:
        raise HTTPException(status_code=422, detail="Invalid pagination cursor record ID")
    return (anchor.created_at, anchor.id)


def execute_collection_query(
    session: Session,
    project: Project,
    collection: Collection,
    filters: list[dict[str, Any]],
    sort_dir: str,
    select_fields: list[str] | None,
    limit: int,
    after: str | None,
) -> dict[str, Any]:
    properties = collection.schema_json.get("properties", {})
    indexed_fields = set(get_indexed_fields(collection))

    for flt in filters:
        fname = flt["field"]
        if fname != "id" and fname not in properties:
            raise HTTPException(status_code=422, detail=f"Unknown filter field: '{fname}'")

    if select_fields is not None:
        unknown_sel = [f for f in select_fields if f not in properties]
        if unknown_sel:
            raise HTTPException(status_code=422, detail=f"Unknown projected fields: {', '.join(unknown_sel)}")

    base_stmt = select(Record).where(
        Record.project_id == project.id,
        Record.collection_id == collection.id,
    )

    used_indexed_fields: list[str] = []
    residual_filters: list[dict[str, Any]] = []

    for flt in filters:
        fname, op, val = flt["field"], flt["op"], flt["value"]
        if fname == "id" and op == "eq" and isinstance(val, str):
            base_stmt = base_stmt.where(Record.id == val)
            continue
        if fname in indexed_fields:
            prop_type = properties[fname]["type"]
            subq, needs_residual = _build_index_subquery(collection.id, prop_type, fname, op, val)
            if subq is not None:
                base_stmt = base_stmt.where(Record.id.in_(subq))
                if fname not in used_indexed_fields:
                    used_indexed_fields.append(fname)
                if needs_residual:
                    residual_filters.append(flt)
                continue
        residual_filters.append(flt)

    cursor_anchor = resolve_anchor(session, project.id, collection.id, after) if after else None
    descending = sort_dir == "desc"

    matched_records: list[dict[str, Any]] = []
    scanned = 0
    chunk_anchor = cursor_anchor

    while len(matched_records) <= limit and scanned < MAX_SCAN_PER_QUERY:
        stmt = base_stmt
        if chunk_anchor is not None:
            if descending:
                stmt = stmt.where(tuple_(Record.created_at, Record.id) < chunk_anchor)
            else:
                stmt = stmt.where(tuple_(Record.created_at, Record.id) > chunk_anchor)

        if descending:
            stmt = stmt.order_by(Record.created_at.desc(), Record.id.desc())
        else:
            stmt = stmt.order_by(Record.created_at.asc(), Record.id.asc())

        fetch_size = min(STREAM_CHUNK_SIZE, max(limit + 1, 100)) if not residual_filters else STREAM_CHUNK_SIZE
        chunk = list(session.scalars(stmt.limit(fetch_size)))
        if not chunk:
            break

        for rec in chunk:
            scanned += 1
            payload = rec.data
            if residual_filters and not all(
                matches_predicate(rec.id, payload, rf["field"], rf["op"], rf["value"]) for rf in residual_filters
            ):
                continue
            projected = {k: payload[k] for k in select_fields if k in payload} if select_fields is not None else payload
            matched_records.append(
                {
                    "id": rec.id,
                    "data": projected,
                    "created_at": rec.created_at,
                    "updated_at": rec.updated_at,
                }
            )
            if len(matched_records) > limit:
                break

        chunk_anchor = (chunk[-1].created_at, chunk[-1].id)
        if len(chunk) < fetch_size:
            break

    next_cursor: str | None = None
    if len(matched_records) > limit:
        matched_records = matched_records[:limit]
        next_cursor = matched_records[-1]["id"]

    return {
        "records": matched_records,
        "next_cursor": next_cursor,
        "returned": len(matched_records),
        "scanned": scanned,
        "index_used": len(used_indexed_fields) > 0,
        "indexed_fields_used": used_indexed_fields,
    }


def stream_collection_ndjson(session: Session, project_id: str, collection_id: str, batch_size: int = 500) -> Iterator[bytes]:
    """Stream an entire collection in constant memory using keyset cursor chunks."""
    cursor_anchor: tuple[Any, str] | None = None
    while True:
        stmt = select(Record).where(
            Record.project_id == project_id,
            Record.collection_id == collection_id,
        )
        if cursor_anchor is not None:
            stmt = stmt.where(tuple_(Record.created_at, Record.id) > cursor_anchor)
        stmt = stmt.order_by(Record.created_at, Record.id).limit(batch_size)
        rows = list(session.scalars(stmt))
        if not rows:
            break
        lines = []
        for rec in rows:
            doc = {
                "id": rec.id,
                "data": rec.data,
                "created_at": rec.created_at.isoformat() if rec.created_at else None,
                "updated_at": rec.updated_at.isoformat() if rec.updated_at else None,
            }
            lines.append(json.dumps(doc, separators=(",", ":"), ensure_ascii=False))
        cursor_anchor = (rows[-1].created_at, rows[-1].id)
        for rec in rows:
            session.expunge(rec)
        yield ("\n".join(lines) + "\n").encode("utf-8")


def compact_collection_records(session: Session, project: Project, collection: Collection) -> dict[str, Any]:
    """Re-encode any uncompressed legacy or updated records to reclaim storage space."""
    recompressed = 0
    cursor_anchor: tuple[Any, str] | None = None
    while True:
        stmt = select(Record).where(
            Record.project_id == project.id,
            Record.collection_id == collection.id,
        )
        if cursor_anchor is not None:
            stmt = stmt.where(tuple_(Record.created_at, Record.id) > cursor_anchor)
        stmt = stmt.order_by(Record.created_at, Record.id).limit(STREAM_CHUNK_SIZE)
        rows = list(session.scalars(stmt))
        if not rows:
            break
        for rec in rows:
            if rec.compressed_data is None and rec.inline_data is not None:
                inline, compressed, raw_len, stored_len = encode_record_payload(rec.inline_data)
                rec.raw_size_bytes = raw_len
                rec.stored_size_bytes = stored_len
                if compressed is not None:
                    rec.inline_data = inline
                    rec.compressed_data = compressed
                    recompressed += 1
        cursor_anchor = (rows[-1].created_at, rows[-1].id)
        session.flush()
    session.commit()

    totals = session.execute(
        select(
            func.count(Record.id),
            func.coalesce(func.sum(Record.raw_size_bytes), 0),
            func.coalesce(func.sum(Record.stored_size_bytes), 0),
        ).where(Record.project_id == project.id, Record.collection_id == collection.id)
    ).one()
    total_records, raw_bytes, stored_bytes = int(totals[0]), int(totals[1]), int(totals[2])
    saved = max(0, raw_bytes - stored_bytes)
    ratio = round(raw_bytes / stored_bytes, 2) if stored_bytes > 0 and raw_bytes > 0 else 1.0
    return {
        "collection": collection.name,
        "total_records": total_records,
        "recompressed_records": recompressed,
        "raw_bytes": raw_bytes,
        "stored_bytes": stored_bytes,
        "saved_bytes": saved,
        "compression_ratio": ratio,
    }


def compute_project_storage_stats(session: Session, project: Project) -> dict[str, Any]:
    """Compute real, measured storage density, compression savings, and shard balance."""
    collections = list(
        session.scalars(select(Collection).where(Collection.project_id == project.id).order_by(Collection.name))
    )
    agg_rows = session.execute(
        select(
            Record.collection_id,
            func.count(Record.id),
            func.coalesce(func.sum(Record.raw_size_bytes), 0),
            func.coalesce(func.sum(Record.stored_size_bytes), 0),
            func.count(Record.compressed_data),
        )
        .where(Record.project_id == project.id)
        .group_by(Record.collection_id)
    ).all()
    by_coll = {
        row[0]: {
            "records": int(row[1]),
            "raw_bytes": int(row[2]),
            "stored_bytes": int(row[3]),
            "compressed_records": int(row[4]),
        }
        for row in agg_rows
    }

    shard_rows = session.execute(
        select(Record.shard_key, func.count(Record.id))
        .where(Record.project_id == project.id)
        .group_by(Record.shard_key)
    ).all()
    active_shards = len(shard_rows)

    total_records = 0
    total_compressed = 0
    total_raw = 0
    total_stored = 0
    total_indexes = 0
    coll_stats = []

    for coll in collections:
        m = by_coll.get(coll.id, {"records": 0, "raw_bytes": 0, "stored_bytes": 0, "compressed_records": 0})
        idx_fields = get_indexed_fields(coll)
        total_indexes += len(idx_fields)
        raw_b = m["raw_bytes"]
        stored_b = m["stored_bytes"]
        saved_b = max(0, raw_b - stored_b)
        ratio = round(raw_b / stored_b, 2) if stored_b > 0 and raw_b > 0 else 1.0
        total_records += m["records"]
        total_compressed += m["compressed_records"]
        total_raw += raw_b
        total_stored += stored_b
        coll_stats.append(
            {
                "id": coll.id,
                "name": coll.name,
                "records": m["records"],
                "compressed_records": m["compressed_records"],
                "raw_bytes": raw_b,
                "stored_bytes": stored_b,
                "saved_bytes": saved_b,
                "compression_ratio": ratio,
                "indexes": idx_fields,
            }
        )

    total_saved = max(0, total_raw - total_stored)
    overall_ratio = round(total_raw / total_stored, 2) if total_stored > 0 and total_raw > 0 else 1.0

    return {
        "project_id": project.id,
        "total_collections": len(collections),
        "total_records": total_records,
        "compressed_records": total_compressed,
        "raw_bytes": total_raw,
        "stored_bytes": total_stored,
        "saved_bytes": total_saved,
        "compression_ratio": overall_ratio,
        "indexed_fields_count": total_indexes,
        "virtual_shards": SHARD_BUCKETS,
        "active_shards": active_shards,
        "collections": coll_stats,
        "engine": get_engine_stats(),
    }
