<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import type { DataRecord, JsonObject } from '@kestrel/client';
import { api, state, currentProject, notify, errorMessage } from '../lib/store';
import { downloadJson, parseObject, sampleData } from '../lib/schema';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
const route = useRoute(); const router = useRouter();
const selected = ref(String(route.query.collection ?? '')); const records = ref<DataRecord[]>([]); const offset = ref(0); const search = ref('');
const loading = ref(false); const busy = ref(false); const error = ref(''); const editor = ref(false); const editing = ref<DataRecord | null>(null); const json = ref('{}'); const deleting = ref<DataRecord | null>(null);
let controller: AbortController | undefined;
const pageSize = 25;
const columns = computed(() => Object.keys(state.collections.find(c => c.name === selected.value)?.schema.properties ?? {}).slice(0, 5));
const filtered = computed(() => records.value.filter(r => `${r.id} ${JSON.stringify(r.data)}`.toLowerCase().includes(search.value.toLowerCase())));
watch(() => [state.projectId, state.collections.map(c => c.name).join('|')], () => {
  if (!state.collections.some(c => c.name === selected.value)) selected.value = state.collections[0]?.name ?? '';
}, { immediate: true });
watch(selected, (name) => { offset.value = 0; search.value = ''; if (name) router.replace({ path: '/records', query: { collection: name } }); });
watch(() => [state.projectId, selected.value, offset.value], load, { immediate: true });
async function load() {
  controller?.abort(); const current = new AbortController(); controller = current;
  records.value = []; error.value = '';
  if (!selected.value || !currentProject.value?.api_enabled) return;
  loading.value = true;
  try { const result = await api.value.listRecords(state.projectId, selected.value, { limit: pageSize, offset: offset.value, signal: current.signal }); if (!current.signal.aborted) records.value = result; }
  catch (e) { if (!current.signal.aborted) error.value = errorMessage(e); }
  finally { if (!current.signal.aborted) loading.value = false; }
}
onBeforeUnmount(() => controller?.abort());
function openEditor(record: DataRecord | null = null) {
  editing.value = record; error.value = '';
  const collection = state.collections.find(c => c.name === selected.value);
  json.value = JSON.stringify(record?.data ?? (collection ? sampleData(collection.schema) : {}), null, 2); editor.value = true;
}
async function save() {
  busy.value = true; error.value = '';
  try { const data = parseObject(json.value); if (editing.value) await api.value.updateRecord(state.projectId, selected.value, editing.value.id, data); else await api.value.createRecord(state.projectId, selected.value, data); editor.value = false; await load(); notify(editing.value ? 'Record updated.' : 'Record added to your collection.'); }
  catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
async function remove() {
  if (!deleting.value) return; busy.value = true; error.value = '';
  try { await api.value.deleteRecord(state.projectId, selected.value, deleting.value.id); deleting.value = null; if (records.value.length === 1 && offset.value) offset.value -= pageSize; await load(); notify('Record deleted.'); }
  catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
function display(value: JsonValue) { return value === null ? 'null' : typeof value === 'object' ? JSON.stringify(value) : String(value); }
type JsonValue = JsonObject[string];
</script>
<template>
  <div class="page-heading"><div><div class="eyebrow">A CLOSER LOOK AT YOUR DATA</div><h1>Data explorer<span class="heading-dot">.</span></h1><p>Browse, shape, and understand the records in your collections.</p></div><button class="button primary" :disabled="!selected || !currentProject?.api_enabled" @click="openEditor()"><Icon name="plus" :size="17" />Add record</button></div>
  <div v-if="!selected" class="panel empty-state"><Icon name="table" :size="36" /><h2>Your first record starts with a collection</h2><p>Create a collection or enable this project’s data API to begin.</p><RouterLink to="/collections" class="button primary">Go to collections</RouterLink></div>
  <section v-else class="panel records-panel">
    <div class="records-tabs"><div class="collection-tab"><Icon name="table" :size="17" /><select v-model="selected" aria-label="Select collection"><option v-for="collection in state.collections" :key="collection.id">{{ collection.name }}</option></select><span class="count-badge">{{ records.length }}{{ records.length === pageSize ? '+' : '' }}</span></div><span class="table-view"><Icon name="grid" :size="15" />Table view</span></div><div class="toolbar record-toolbar"><div class="search-input"><Icon name="search" :size="17" /><input v-model="search" placeholder="Search this page…" aria-label="Search records on this page" /></div><div class="toolbar-actions"><button class="button secondary small-button" :disabled="!records.length" @click="downloadJson(`${selected}-page-${offset / pageSize + 1}.json`, records)"><Icon name="download" :size="15" />Export page</button><button class="icon-button bordered" aria-label="Refresh records" :disabled="loading" @click="load"><Icon name="bolt" :size="16" /></button></div></div>
    <p v-if="error && !editor && !deleting" class="error-box inline-error" role="alert">{{ error }}</p>
    <div class="table-scroll"><table class="data-table"><thead><tr><th class="row-number">#</th><th>id <span class="column-type">string</span></th><th v-for="column in columns" :key="column">{{ column }} <span class="column-type">{{ state.collections.find(c => c.name === selected)?.schema.properties[column]?.type }}</span></th><th class="actions-cell">Actions</th></tr></thead><tbody><tr v-for="(record, index) in filtered" :key="record.id"><td class="row-number">{{ offset + index + 1 }}</td><td><code class="record-id" :title="record.id">{{ record.id }}</code></td><td v-for="column in columns" :key="column"><span v-if="typeof record.data[column] === 'boolean'" class="pill" :class="{ neutral: !record.data[column] }">{{ record.data[column] }}</span><span v-else class="cell-value" :title="display(record.data[column] ?? null)">{{ record.data[column] === undefined ? '—' : display(record.data[column]!) }}</span></td><td class="actions-cell"><button class="icon-button" :aria-label="`Edit record ${record.id}`" @click="openEditor(record)"><Icon name="edit" :size="16" /></button><button class="icon-button danger-text" :aria-label="`Delete record ${record.id}`" @click="deleting = record; error = ''"><Icon name="trash" :size="16" /></button></td></tr></tbody></table></div>
    <div v-if="loading" class="empty-state"><span class="spinner"></span><p>Loading records…</p></div><div v-else-if="!filtered.length" class="empty-state"><span class="empty-icon"><Icon name="table" :size="30" /></span><h2>{{ search ? 'No matching records' : 'A blank canvas, for now.' }}</h2><p>{{ search ? 'Try another search, or clear the filter.' : 'Add your first record and give this collection some life.' }}</p><button v-if="!search" class="button secondary" @click="openEditor()"><Icon name="plus" :size="16" />Add a record</button></div>
    <div class="table-footer"><span>Showing {{ records.length ? offset + 1 : 0 }}–{{ offset + records.length }} <span v-if="search">· {{ filtered.length }} matches on this page</span></span><div><button class="button secondary small-button" :disabled="offset === 0 || loading" @click="offset -= pageSize">Previous</button><button class="button secondary small-button" :disabled="records.length < pageSize || loading" @click="offset += pageSize">Next <Icon name="arrow" :size="14" /></button></div></div>
  </section>
  <div class="below-table"><Icon name="shield" :size="16" /><span>Every write is validated against your collection schema. Export includes the current page, not the entire collection.</span></div>
  <Modal v-if="editor" :title="editing ? 'Edit record' : 'Add a record'" wide @close="editor = false"><p class="muted">{{ editing ? 'Update the JSON below. Fields are merged and validated on save.' : `Add a JSON object to ${selected}. Required fields must be included.` }}</p><form @submit.prevent="save"><label class="editor-label">Record data <span>JSON · max 64 KiB</span><textarea v-model="json" class="json-editor" spellcheck="false" rows="14" required aria-label="Record JSON"></textarea></label><p v-if="error" class="error-box" role="alert">{{ error }}</p><div class="modal-actions"><button type="button" class="button secondary" @click="editor = false">Cancel</button><button class="button primary" :disabled="busy">{{ busy ? 'Saving…' : editing ? 'Save changes' : 'Create record' }}</button></div></form></Modal>
  <Modal v-if="deleting" title="Delete this record?" @close="deleting = null"><p>This permanently removes the record from <strong>{{ selected }}</strong>. This cannot be undone.</p><code class="delete-id">{{ deleting.id }}</code><p v-if="error" class="error-box" role="alert">{{ error }}</p><div class="modal-actions"><button class="button secondary" @click="deleting = null">Keep record</button><button class="button danger" :disabled="busy" @click="remove">{{ busy ? 'Deleting…' : 'Delete record' }}</button></div></Modal>
</template>
