<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import type { DataRecord, FilterOperator, Json } from '@kestrel/client';
import { api, state, currentProject, loadStorage, notify, errorMessage } from '../lib/store';
import { downloadJson, downloadText, formatBytes, generateSampleBatch, parseBatchInput, parseObject, sampleData } from '../lib/schema';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';

const route = useRoute();
const router = useRouter();
const collectionName = ref('');
const records = ref<DataRecord[]>([]);
const loading = ref(false);
const error = ref('');
const modalError = ref('');
const query = ref('');

// Keyset cursor stack for O(1) deep pagination across large datasets
const limit = 25;
const cursorStack = ref<(string | undefined)[]>([undefined]);
const nextCursor = ref<string | null>(null);
const hasMore = ref(false);

// Server-side indexed query controls
const showQueryBar = ref(false);
const filterField = ref('');
const filterOp = ref<FilterOperator>('eq');
const filterRawValue = ref('');
const sortDir = ref<'asc' | 'desc'>('asc');
const activeServerQuery = ref(false);
const queryMeta = ref<{ scanned: number; indexUsed: boolean; indexedFields: string[] } | null>(null);

// Modals: single record editor, delete confirmation, and bulk batch / NDJSON ingest
const editing = ref<DataRecord | null>(null);
const adding = ref(false);
const deleting = ref<DataRecord | null>(null);
const bulkOpen = ref(false);
const bulkText = ref('');
const bulkBusy = ref(false);
const exportBusy = ref(false);
const editor = ref('{}');
const busy = ref(false);
let recordLoad = 0;

const collection = computed(() => state.collections.find((c) => c.name === collectionName.value));
const collectionStats = computed(() => state.storage?.collections.find((c) => c.name === collectionName.value));
const columns = computed(() => Object.keys(collection.value?.schema.properties ?? {}));
const pageIndex = computed(() => cursorStack.value.length - 1);
const filtered = computed(() => {
  const q = query.value.trim().toLowerCase();
  return q ? records.value.filter((r) => r.id.toLowerCase().includes(q) || JSON.stringify(r.data).toLowerCase().includes(q)) : records.value;
});

function coerceFilterValue(field: string, op: FilterOperator, raw: string): Json {
  if (op === 'in') {
    return raw.split(',').map((part) => coerceScalarField(field, part.trim()));
  }
  return coerceScalarField(field, raw);
}

function coerceScalarField(field: string, raw: string): Json {
  const t = collection.value?.schema.properties[field]?.type;
  if (t === 'integer') {
    const n = Number.parseInt(raw, 10);
    if (Number.isNaN(n)) throw new Error(`Filter value for ${field} must be an integer.`);
    return n;
  }
  if (t === 'number') {
    const n = Number(raw);
    if (Number.isNaN(n)) throw new Error(`Filter value for ${field} must be a number.`);
    return n;
  }
  if (t === 'boolean') {
    if (raw === 'true') return true;
    if (raw === 'false') return false;
    throw new Error(`Filter value for ${field} must be true or false.`);
  }
  return raw;
}

watch([() => state.collections, () => route.query.collection], () => {
  const wanted = typeof route.query.collection === 'string' ? route.query.collection : '';
  const next = state.collections.find((c) => c.name === wanted)?.name ?? state.collections.find((c) => c.name === collectionName.value)?.name ?? state.collections[0]?.name ?? '';
  if (next !== collectionName.value) {
    collectionName.value = next;
    cursorStack.value = [undefined];
    activeServerQuery.value = false;
    queryMeta.value = null;
    filterField.value = Object.keys(state.collections.find((c) => c.name === next)?.schema.properties ?? {})[0] ?? '';
  }
  void load();
}, { immediate: true });

async function load() {
  if (!state.projectId || !collection.value) { records.value = []; return; }
  const current = ++recordLoad;
  const projectId = state.projectId;
  const name = collection.value.name;
  const cursor = cursorStack.value[cursorStack.value.length - 1];
  loading.value = true; error.value = '';
  try {
    if (activeServerQuery.value) {
      const filters = filterField.value && filterRawValue.value.trim() !== ''
        ? [{ field: filterField.value, op: filterOp.value, value: coerceFilterValue(filterField.value, filterOp.value, filterRawValue.value.trim()) }]
        : [];
      const res = await api.value.queryRecords(projectId, name, {
        filters,
        sort_dir: sortDir.value,
        limit,
        after: cursor,
      });
      if (current === recordLoad) {
        records.value = res.records;
        nextCursor.value = res.next_cursor;
        hasMore.value = res.next_cursor !== null;
        queryMeta.value = { scanned: res.scanned, indexUsed: res.index_used, indexedFields: res.indexed_fields_used };
      }
    } else {
      const data = await api.value.listRecords(projectId, name, { limit: limit + 1, offset: 0, after: cursor });
      if (current === recordLoad) {
        hasMore.value = data.length > limit;
        const page = data.slice(0, limit);
        records.value = page;
        nextCursor.value = hasMore.value && page.length ? page[page.length - 1]!.id : null;
        queryMeta.value = null;
      }
    }
  } catch (e) { if (current === recordLoad) error.value = errorMessage(e); }
  finally { if (current === recordLoad) loading.value = false; }
}

function goNextPage() {
  if (!hasMore.value || !nextCursor.value) return;
  cursorStack.value.push(nextCursor.value);
  void load();
}
function goPrevPage() {
  if (cursorStack.value.length <= 1) return;
  cursorStack.value.pop();
  void load();
}
function runServerQuery() {
  cursorStack.value = [undefined];
  activeServerQuery.value = true;
  void load();
}
function clearServerQuery() {
  filterRawValue.value = '';
  sortDir.value = 'asc';
  activeServerQuery.value = false;
  cursorStack.value = [undefined];
  void load();
}
function openAdd() { if (!collection.value) return; adding.value = true; editing.value = null; modalError.value = ''; editor.value = JSON.stringify(sampleData(collection.value.schema), null, 2); }
function openEdit(record: DataRecord) { editing.value = record; adding.value = false; modalError.value = ''; editor.value = JSON.stringify(record.data, null, 2); }
function openBulk() {
  if (!collection.value) return;
  bulkOpen.value = true;
  modalError.value = '';
  const sample = generateSampleBatch(collection.value.schema, 3);
  bulkText.value = sample.map((item) => JSON.stringify(item)).join('\n');
}
function fillSampleBatch(count: number) {
  if (!collection.value) return;
  modalError.value = '';
  bulkText.value = generateSampleBatch(collection.value.schema, count).map((item) => JSON.stringify(item)).join('\n');
}
async function handleFileUpload(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;
  bulkText.value = await file.text();
  input.value = '';
}
async function submitBulk() {
  if (!collection.value || !state.projectId) return;
  bulkBusy.value = true; modalError.value = '';
  try {
    const items = parseBatchInput(bulkText.value);
    const res = await api.value.batchCreateRecords(state.projectId, collection.value.name, items);
    bulkOpen.value = false;
    cursorStack.value = [undefined];
    await Promise.all([load(), loadStorage()]);
    notify(`Ingested ${res.inserted.toLocaleString()} records (${res.compression_ratio}× compression · ${formatBytes(res.saved_bytes)} saved).`);
  } catch (e) {
    modalError.value = errorMessage(e);
  } finally {
    bulkBusy.value = false;
  }
}
async function exportFullNdjson() {
  if (!collection.value || !state.projectId) return;
  exportBusy.value = true;
  try {
    const ndjson = await api.value.exportRecordsNdjson(state.projectId, collection.value.name);
    downloadText(`${collection.value.name}.ndjson`, ndjson);
    const lineCount = ndjson.trim() ? ndjson.trim().split('\n').length : 0;
    notify(`Streamed ${lineCount.toLocaleString()} records as NDJSON.`);
  } catch (e) {
    error.value = errorMessage(e);
  } finally {
    exportBusy.value = false;
  }
}
async function save() {
  if (!collection.value || !state.projectId) return;
  busy.value = true; modalError.value = '';
  try {
    const data = parseObject(editor.value);
    if (editing.value) await api.value.updateRecord(state.projectId, collection.value.name, editing.value.id, data);
    else await api.value.createRecord(state.projectId, collection.value.name, data);
    adding.value = false; editing.value = null;
    await Promise.all([load(), loadStorage()]);
    notify(editing.value ? 'Record updated.' : 'Record created.');
  } catch (e) { modalError.value = errorMessage(e); } finally { busy.value = false; }
}
async function remove() {
  if (!collection.value || !deleting.value || !state.projectId) return;
  busy.value = true;
  try {
    await api.value.deleteRecord(state.projectId, collection.value.name, deleting.value.id);
    deleting.value = null;
    if (records.value.length === 1 && cursorStack.value.length > 1) cursorStack.value.pop();
    await Promise.all([load(), loadStorage()]);
    notify('Record deleted.');
  } catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
function formatCell(value: unknown) { if (value === undefined) return '—'; if (value === null) return 'null'; return typeof value === 'object' ? JSON.stringify(value) : String(value); }
function chooseCollection(name: string) {
  collectionName.value = name;
  cursorStack.value = [undefined];
  activeServerQuery.value = false;
  filterField.value = Object.keys(state.collections.find((c) => c.name === name)?.schema.properties ?? {})[0] ?? '';
  void router.replace({ query: { collection: name } });
}
</script>
<template>
  <div class="page-heading">
    <div><div class="eyebrow">EVERY RECORD, ACCOUNTED FOR</div><h1>Data explorer<span class="heading-dot">.</span></h1><p>Browse with O(1) keyset cursors, filter via B-tree indexes, bulk-ingest batches, and stream NDJSON datasets.</p></div>
    <div class="heading-actions">
      <button class="button secondary" :disabled="!collection" @click="openBulk"><Icon name="upload" :size="15" />Bulk ingest</button>
      <button class="button primary" :disabled="!collection" @click="openAdd"><Icon name="plus" :size="16" />Add record</button>
    </div>
  </div>
  <div v-if="!currentProject" class="panel empty-state"><h2>Select a project first</h2><p>Choose a project to explore its collections and records.</p><RouterLink to="/projects" class="button primary">Go to projects</RouterLink></div>
  <div v-else-if="!currentProject.api_enabled" class="panel empty-state"><h2>Data API is turned off</h2><p>Enable the project Data API in settings to read or write records.</p><RouterLink to="/settings" class="button secondary">Open project settings</RouterLink></div>
  <div v-else-if="!state.collections.length && !state.loading" class="panel empty-state"><h2>Create a collection first</h2><p>Records live inside a typed collection schema.</p><RouterLink to="/collections" class="button primary">Create collection</RouterLink></div>
  <section v-else class="panel records-panel">
    <div class="records-tabs">
      <div class="collection-tab"><Icon name="table" :size="16" /><select :value="collectionName" aria-label="Select collection" @change="chooseCollection(($event.target as HTMLSelectElement).value)"><option v-for="c in state.collections" :key="c.id" :value="c.name">{{ c.name }}</option></select><span class="count-badge">{{ collectionStats ? `${collectionStats.records.toLocaleString()} total` : `${records.length} shown` }}</span></div>
      <div class="table-view">
        <span v-if="collectionStats && collectionStats.records > 0" class="pill">{{ collectionStats.compression_ratio }}× compressed · {{ formatBytes(collectionStats.stored_bytes) }}</span>
        <Icon name="grid" :size="15" />Table view
      </div>
    </div>
    <div class="toolbar record-toolbar">
      <label class="search-input"><Icon name="search" :size="16" /><input v-model="query" placeholder="Search current page…" aria-label="Search records on this page" /></label>
      <div class="toolbar-actions">
        <button class="button secondary small-button" :class="{ active: showQueryBar || activeServerQuery }" @click="showQueryBar = !showQueryBar">
          <Icon name="filter" :size="14" /><span>{{ activeServerQuery ? 'Filtered' : 'Query' }}</span>
        </button>
        <button class="button secondary small-button" :disabled="!records.length" @click="downloadJson(`${collectionName}-page-${pageIndex + 1}.json`, filtered)"><Icon name="download" :size="15" /><span>Export page</span></button>
        <button class="button secondary small-button" :disabled="exportBusy" @click="exportFullNdjson"><Icon name="layers" :size="14" /><span>{{ exportBusy ? 'Streaming…' : 'Export NDJSON' }}</span></button>
      </div>
    </div>
    <form v-if="showQueryBar && collection" class="query-bar" @submit.prevent="runServerQuery">
      <select v-model="filterField" aria-label="Query field">
        <option v-for="col in columns" :key="col" :value="col">{{ col }}{{ collection.schema.indexes?.includes(col) ? ' (indexed)' : '' }}</option>
      </select>
      <select v-model="filterOp" aria-label="Query operator">
        <option value="eq">=</option>
        <option value="neq">!=</option>
        <option value="prefix">starts with</option>
        <option value="contains">contains</option>
        <option value="gte">&gt;=</option>
        <option value="lte">&lt;=</option>
        <option value="gt">&gt;</option>
        <option value="lt">&lt;</option>
        <option value="in">in (comma list)</option>
      </select>
      <input v-model="filterRawValue" placeholder="Filter value (optional)…" aria-label="Query value" />
      <select v-model="sortDir" aria-label="Sort direction">
        <option value="asc">Oldest first</option>
        <option value="desc">Newest first</option>
      </select>
      <button type="submit" class="button primary small-button">Run query</button>
      <button v-if="activeServerQuery" type="button" class="button secondary small-button" @click="clearServerQuery">Reset</button>
      <span v-if="queryMeta" class="pill" :class="{ neutral: !queryMeta.indexUsed }">
        <span :class="['status-dot', queryMeta.indexUsed ? '' : 'amber']"></span>
        {{ queryMeta.indexUsed ? `B-tree index (${queryMeta.indexedFields.join(', ')})` : `Scanned ${queryMeta.scanned}` }}
      </span>
    </form>
    <p v-if="error" class="error-box inline-error" role="alert">{{ error }}</p>
    <div v-if="loading" class="empty-state"><span class="spinner" aria-hidden="true"></span><p>Loading records…</p></div>
    <div v-else-if="!filtered.length" class="empty-state"><h2>{{ records.length ? 'No matching records' : 'No records yet' }}</h2><p>{{ records.length ? 'Try clearing the page search or switching pages.' : 'Add a validated JSON record or bulk-ingest a batch to see it in this table.' }}</p><div class="empty-actions"><button v-if="!records.length" class="button secondary" @click="openBulk"><Icon name="upload" :size="15" />Bulk ingest</button><button v-if="!records.length" class="button primary" @click="openAdd"><Icon name="plus" :size="16" />Add first record</button></div></div>
    <div v-else class="table-scroll">
      <table class="data-table">
        <thead><tr><th class="row-number">#</th><th>id</th><th v-for="col in columns" :key="col">{{ col }}<span class="column-type">{{ collection?.schema.properties[col]?.type }}</span></th><th>updated</th><th class="actions-cell">Actions</th></tr></thead>
        <tbody>
          <tr v-for="(record, idx) in filtered" :key="record.id">
            <td class="row-number">{{ pageIndex * limit + idx + 1 }}</td>
            <td><code class="record-id">{{ record.id }}</code></td>
            <td v-for="col in columns" :key="col"><span class="cell-value">{{ formatCell(record.data[col]) }}</span></td>
            <td class="muted small">{{ record.updated_at.slice(0, 10) }}</td>
            <td class="actions-cell"><button class="icon-button" :aria-label="`Edit record ${record.id}`" @click="openEdit(record)"><Icon name="edit" :size="16" /></button><button class="icon-button danger-text" :aria-label="`Delete record ${record.id}`" @click="deleting = record"><Icon name="trash" :size="16" /></button></td>
          </tr>
        </tbody>
      </table>
    </div>
    <footer class="table-footer"><span>Page {{ pageIndex + 1 }} · Showing {{ filtered.length }} of {{ records.length }} loaded records (keyset cursor)</span><div><button class="button secondary small-button" :disabled="pageIndex === 0 || loading" @click="goPrevPage">Previous</button><button class="button secondary small-button" :disabled="!hasMore || loading" @click="goNextPage">Next</button></div></footer>
  </section>
  <p class="below-table"><Icon name="shield" :size="15" /><span>Records are stored with transparent Zlib payload compression (<code>KZ1</code>) and paginated via O(1) keyset cursors. Use <strong>Export NDJSON</strong> to stream full collections in constant memory.</span></p>
  <Modal v-if="adding || editing" :title="editing ? 'Edit record' : 'Add record'" wide @close="adding = false; editing = null">
    <p>Provide a JSON object matching the <strong>{{ collectionName }}</strong> schema (up to 512 KiB per record).</p>
    <form @submit.prevent="save">
      <label class="editor-label">Record JSON<span>Fields: {{ columns.join(', ') }}</span><textarea v-model="editor" rows="10" class="json-editor" spellcheck="false" required></textarea></label>
      <p v-if="modalError" class="error-box" role="alert">{{ modalError }}</p>
      <div class="modal-actions"><button type="button" class="button secondary" @click="adding = false; editing = null">Cancel</button><button class="button primary" :disabled="busy">{{ busy ? 'Saving…' : editing ? 'Save changes' : 'Create record' }}</button></div>
    </form>
  </Modal>
  <Modal v-if="bulkOpen" title="Bulk batch & NDJSON ingest" wide @close="bulkOpen = false">
    <p>Ingest up to <strong>5,000 records per batch</strong> into <strong>{{ collectionName }}</strong> as a JSON array or newline-delimited JSON (<code>.ndjson</code>).</p>
    <div class="batch-presets">
      <button type="button" class="button secondary small-button" @click="fillSampleBatch(100)">Generate 100 rows</button>
      <button type="button" class="button secondary small-button" @click="fillSampleBatch(500)">Generate 500 rows</button>
      <button type="button" class="button secondary small-button" @click="fillSampleBatch(2000)">Generate 2,000 rows</button>
      <label class="button secondary small-button file-upload-btn">
        <Icon name="upload" :size="14" />Load .ndjson / .json
        <input type="file" accept=".json,.ndjson,.jsonl,application/json,text/plain" hidden @change="handleFileUpload" />
      </label>
    </div>
    <form @submit.prevent="submitBulk">
      <label class="editor-label">Batch payload (NDJSON or JSON array)<span>Schema: {{ columns.join(', ') }}</span><textarea v-model="bulkText" rows="10" class="json-editor" spellcheck="false" required></textarea></label>
      <p v-if="modalError" class="error-box" role="alert">{{ modalError }}</p>
      <div class="modal-actions"><button type="button" class="button secondary" @click="bulkOpen = false">Cancel</button><button class="button primary" :disabled="bulkBusy">{{ bulkBusy ? 'Ingesting…' : 'Ingest batch' }}</button></div>
    </form>
  </Modal>
  <Modal v-if="deleting" title="Delete record?" @close="deleting = null">
    <p>Delete record <code>{{ deleting.id }}</code> from <strong>{{ collectionName }}</strong>? This cannot be undone.</p>
    <code class="delete-id">{{ JSON.stringify(deleting.data) }}</code>
    <div class="modal-actions"><button class="button secondary" @click="deleting = null">Cancel</button><button class="button danger" :disabled="busy" @click="remove">Delete record</button></div>
  </Modal>
</template>
