<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { api, state, currentProject, loadCollections, notify, errorMessage } from '../lib/store';
import { buildSchema, fieldTypes, formatBytes, indexableTypes, type Field } from '../lib/schema';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';

const selectedName = ref('');
const creating = ref(false);
const deleting = ref(false);
const name = ref('');
const fields = ref<Field[]>([{ name: 'title', type: 'string', required: true, indexed: true }]);
const error = ref('');
const busy = ref(false);
const indexBusy = ref('');
const compactBusy = ref(false);

const selected = computed(() => state.collections.find((c) => c.name === selectedName.value) ?? state.collections[0]);
const selectedStats = computed(() => state.storage?.collections.find((c) => c.name === selected.value?.name));
const indexedFields = computed(() => new Set(selected.value?.schema.indexes ?? []));

watch(() => state.collections, (list) => {
  if (!list.some((item) => item.name === selectedName.value)) selectedName.value = list[0]?.name ?? '';
}, { immediate: true });

function resetForm() {
  name.value = '';
  fields.value = [{ name: 'title', type: 'string', required: true, indexed: false }];
  error.value = '';
}
async function create() {
  if (!state.projectId) return;
  busy.value = true; error.value = '';
  try {
    const created = await api.value.createCollection(state.projectId, { name: name.value.trim(), schema: buildSchema(fields.value) });
    await loadCollections(); selectedName.value = created.name; creating.value = false; resetForm(); notify('Collection created.');
  } catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
async function remove() {
  if (!state.projectId || !selected.value) return;
  busy.value = true;
  try { await api.value.deleteCollection(state.projectId, selected.value.name); deleting.value = false; await loadCollections(); notify('Collection deleted.'); } catch (e) { state.error = errorMessage(e); } finally { busy.value = false; }
}
async function toggleIndex(field: string) {
  if (!state.projectId || !selected.value) return;
  indexBusy.value = field;
  try {
    if (indexedFields.value.has(field)) {
      await api.value.deleteIndex(state.projectId, selected.value.name, field);
      notify(`Dropped secondary index on ${field}.`);
    } else {
      const res = await api.value.createIndex(state.projectId, selected.value.name, field);
      notify(`Indexed ${field} (${res.indexed_entries.toLocaleString()} entries).`);
    }
    await loadCollections();
  } catch (e) {
    state.error = errorMessage(e);
  } finally {
    indexBusy.value = '';
  }
}
async function compact() {
  if (!state.projectId || !selected.value) return;
  compactBusy.value = true;
  try {
    const res = await api.value.compactCollection(state.projectId, selected.value.name);
    await loadCollections();
    notify(`Compacted ${res.total_records.toLocaleString()} records (${res.compression_ratio}× compression ratio).`);
  } catch (e) {
    state.error = errorMessage(e);
  } finally {
    compactBusy.value = false;
  }
}
</script>
<template>
  <div class="page-heading">
    <div><div class="eyebrow">DATA WITH CLEAR SHAPE</div><h1>Collections<span class="heading-dot">.</span></h1><p>Define closed JSON schemas up to 128 fields, manage secondary B-tree indexes, and compact collection storage.</p></div>
    <button class="button primary" :disabled="!currentProject?.api_enabled" @click="creating = true; resetForm()"><Icon name="plus" :size="16" />New collection</button>
  </div>
  <div v-if="!currentProject" class="panel empty-state"><h2>Select a project first</h2><p>Collections belong to a project so their schemas, indexes, and records stay isolated.</p><RouterLink to="/projects" class="button primary">Go to projects</RouterLink></div>
  <div v-else-if="!currentProject.api_enabled" class="panel empty-state"><h2>Data API is turned off</h2><p>Enable the project Data API in settings before managing collections.</p><RouterLink to="/settings" class="button secondary">Open project settings</RouterLink></div>
  <div v-else-if="!state.collections.length && !state.loading" class="panel empty-state"><span class="empty-icon"><Icon name="grid" :size="26" /></span><h2>No collections yet</h2><p>Create a collection with typed fields and secondary indexes to start ingesting records.</p><button class="button primary" @click="creating = true; resetForm()"><Icon name="plus" :size="16" />Create collection</button></div>
  <div v-else class="collection-layout">
    <aside class="panel collection-list" aria-label="Collections">
      <div class="panel-heading"><h2>Collections</h2><span class="count-badge">{{ state.collections.length }}</span></div>
      <button v-for="c in state.collections" :key="c.id" :class="['collection-item', { active: selected?.id === c.id }]" @click="selectedName = c.name">
        <Icon name="table" :size="17" />
        <div>
          <strong>{{ c.name }}</strong>
          <small>{{ Object.keys(c.schema.properties).length }} fields<template v-if="c.schema.indexes?.length"> · {{ c.schema.indexes.length }} idx</template></small>
        </div>
        <Icon name="arrow" :size="15" />
      </button>
    </aside>
    <section v-if="selected" class="panel schema-panel">
      <div class="panel-heading">
        <div>
          <span class="eyebrow">COLLECTION SCHEMA</span>
          <h2>{{ selected.name }}</h2>
          <p v-if="selectedStats">
            {{ selectedStats.records.toLocaleString() }} records · {{ formatBytes(selectedStats.stored_bytes) }} stored vs {{ formatBytes(selectedStats.raw_bytes) }} raw ({{ selectedStats.compression_ratio }}×)
          </p>
        </div>
        <div class="collection-actions">
          <button class="button secondary small-button" :disabled="compactBusy" @click="compact">
            <Icon name="layers" :size="14" />{{ compactBusy ? 'Compacting…' : 'Compact' }}
          </button>
          <RouterLink :to="`/records?collection=${encodeURIComponent(selected.name)}`" class="button secondary small-button">View records <Icon name="arrow" :size="14" /></RouterLink>
          <button class="icon-button danger-text" :aria-label="`Delete collection ${selected.name}`" @click="deleting = true"><Icon name="trash" :size="17" /></button>
        </div>
      </div>
      <div class="schema-table schema-table-indexed" role="table" aria-label="Schema fields">
        <div class="schema-row schema-head" role="row"><span>Field</span><span>Type</span><span>Requirement</span><span>Secondary index</span></div>
        <div v-for="(spec, key) in selected.schema.properties" :key="key" class="schema-row" role="row">
          <strong>{{ key }}</strong>
          <code class="type-badge">{{ spec.type }}</code>
          <span :class="['pill', selected.schema.required.includes(String(key)) ? '' : 'neutral']">{{ selected.schema.required.includes(String(key)) ? 'Required' : 'Optional' }}</span>
          <span class="schema-index-cell">
            <template v-if="indexableTypes.has(spec.type)">
              <span v-if="indexedFields.has(String(key))" class="pill"><span class="status-dot"></span>B-tree</span>
              <button
                type="button"
                class="text-button small-index-btn"
                :disabled="indexBusy === String(key)"
                @click="toggleIndex(String(key))"
              >
                {{ indexBusy === String(key) ? '…' : indexedFields.has(String(key)) ? 'Drop' : '+ Index' }}
              </button>
            </template>
            <span v-else class="muted small">—</span>
          </span>
        </div>
      </div>
      <div class="schema-footer"><span><Icon name="shield" :size="15" />Additional properties are rejected automatically.</span><code>collection id: {{ selected.id }}</code></div>
      <details class="json-details"><summary>View JSON Schema</summary><pre>{{ JSON.stringify(selected.schema, null, 2) }}</pre></details>
    </section>
  </div>
  <Modal v-if="creating" title="Create a collection" wide @close="creating = false">
    <p>Every record added to this collection is validated against these fields and stored with transparent payload compression.</p>
    <form @submit.prevent="create">
      <label>Collection name<input v-model="name" placeholder="products" pattern="^[a-zA-Z][a-zA-Z0-9_-]{1,62}$" required /><span class="field-hint">Start with a letter. Use 2–63 letters, numbers, hyphens, or underscores.</span></label>
      <div class="field-builder">
        <div class="builder-head builder-head-indexed"><span>Field name</span><span>Type</span><span>Req</span><span>Idx</span><span></span></div>
        <div v-for="(field, index) in fields" :key="index" class="builder-row builder-row-indexed">
          <input v-model="field.name" :aria-label="`Field ${index + 1} name`" placeholder="field_name" required />
          <select v-model="field.type" :aria-label="`Field ${index + 1} type`"><option v-for="t in fieldTypes" :key="t" :value="t">{{ t }}</option></select>
          <input v-model="field.required" type="checkbox" :aria-label="`Field ${index + 1} required`" />
          <input v-model="field.indexed" type="checkbox" :disabled="!indexableTypes.has(field.type)" :aria-label="`Field ${index + 1} indexed`" />
          <button type="button" class="icon-button" :disabled="fields.length === 1" :aria-label="`Remove field ${index + 1}`" @click="fields.splice(index, 1)"><Icon name="close" :size="16" /></button>
        </div>
        <button type="button" class="text-button add-field" :disabled="fields.length >= 128" @click="fields.push({ name: '', type: 'string', required: false, indexed: false })"><Icon name="plus" :size="15" />Add field ({{ fields.length }}/128)</button>
      </div>
      <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      <div class="modal-actions"><button type="button" class="button secondary" @click="creating = false">Cancel</button><button class="button primary" :disabled="busy">{{ busy ? 'Creating…' : 'Create collection' }}</button></div>
    </form>
  </Modal>
  <Modal v-if="deleting && selected" title="Delete collection?" @close="deleting = false">
    <p>Delete <strong>{{ selected.name }}</strong> and all records stored in this collection? This cannot be undone.</p>
    <div class="modal-actions"><button class="button secondary" @click="deleting = false">Cancel</button><button class="button danger" :disabled="busy" @click="remove">Delete collection</button></div>
  </Modal>
</template>
