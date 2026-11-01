<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { api, state, currentProject, loadProjects, notify, errorMessage } from '../lib/store';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
const busy = ref(false); const deleting = ref(false); const confirmation = ref(''); const error = ref(''); const copied = ref(false);
const snippet = computed(() => `import { KestrelClient } from '@kestrel/client';\n\nconst db = new KestrelClient({ baseUrl: '/api/' });\nconst session = await db.login({\n  email: 'you@example.com',\n  password: process.env.KESTREL_PASSWORD!,\n});\ndb.setToken(session.access_token);\n\n// Bulk-ingest compressed batches & query via secondary B-tree indexes.\nawait db.batchCreateRecords('${currentProject.value?.id ?? 'PROJECT_ID'}', 'products', [\n  { name: 'Sensor pack', price: 49, in_stock: true },\n]);\nconst result = await db.queryRecords('${currentProject.value?.id ?? 'PROJECT_ID'}', 'products', {\n  filters: [{ field: 'price', op: 'lte', value: 100 }],\n  limit: 100,\n});\n`);
watch(currentProject, () => { confirmation.value = ''; });
async function toggle() {
  if (!currentProject.value) return; busy.value = true;
  try { await api.value.setProjectApi(currentProject.value.id, !currentProject.value.api_enabled); await loadProjects(); notify('Project API setting updated.'); } catch (e) { state.error = errorMessage(e); } finally { busy.value = false; }
}
async function remove() {
  if (!currentProject.value || confirmation.value !== currentProject.value.slug) return;
  busy.value = true; error.value = '';
  try { await api.value.deleteProject(currentProject.value.id); deleting.value = false; await loadProjects(); notify('Project deleted.'); } catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
async function copy() { try { await navigator.clipboard.writeText(snippet.value); copied.value = true; } catch { notify('Clipboard unavailable. Select and copy the example below.'); } }
</script>
<template>
  <div class="page-heading"><div><div class="eyebrow">THE DETAILS THAT KEEP YOU IN CONTROL</div><h1>Project settings<span class="heading-dot">.</span></h1><p>Manage access and connect your application.</p></div></div>
  <div v-if="!currentProject" class="panel empty-state"><h2>No project selected</h2><p>Create a project to manage its settings.</p><RouterLink to="/projects" class="button primary">Go to projects</RouterLink></div>
  <div v-else class="settings-layout">
    <section class="panel settings-panel"><div class="panel-heading"><div><h2>General</h2><p>The essentials for this project.</p></div><Icon name="settings" :size="20" /></div><dl class="settings-details"><div><dt>Project name</dt><dd>{{ currentProject.name }}</dd></div><div><dt>Slug</dt><dd><code>{{ currentProject.slug }}</code></dd></div><div><dt>Project ID</dt><dd><code>{{ currentProject.id }}</code></dd></div></dl><div class="setting-row"><div><strong>Data API</strong><p>Allow collection and record requests for this project.</p></div><button :class="['toggle', { on: currentProject.api_enabled }]" role="switch" :aria-checked="currentProject.api_enabled" aria-label="Enable project data API" :disabled="busy" @click="toggle"><span></span></button></div></section>
    <section class="panel connect-panel"><div class="panel-heading"><div><h2>Connect your application</h2><p>A typed client. No extra runtime dependencies.</p></div><button class="icon-button bordered" aria-label="Copy client example" @click="copy"><Icon :name="copied ? 'check' : 'copy'" :size="17" /></button></div><pre>{{ snippet }}</pre><div class="connect-note"><Icon name="shield" :size="16" /><span>{{ state.demo ? 'Illustrative SDK example. Demo records are not available through the real API.' : 'Keep credentials in environment variables. Browser tokens are kept in memory, not local storage.' }}</span></div></section>
    <section class="panel danger-zone"><div><h2>Delete project</h2><p>Permanently delete this project, its collections, and its control-plane records.</p></div><button class="button danger-outline" @click="deleting = true; error = ''"><Icon name="trash" :size="16" />Delete project</button></section>
  </div>
  <Modal v-if="deleting && currentProject" title="Delete this project?" @close="deleting = false"><p>This permanently deletes <strong>{{ currentProject.name }}</strong>, including all collections and control-plane records. This cannot be undone.</p><form @submit.prevent="remove"><label>Type <strong>{{ currentProject.slug }}</strong> to confirm<input v-model="confirmation" autocomplete="off" /></label><p v-if="error" class="error-box" role="alert">{{ error }}</p><div class="modal-actions"><button type="button" class="button secondary" @click="deleting = false">Keep project</button><button class="button danger" :disabled="busy || confirmation !== currentProject.slug">Delete project</button></div></form></Modal>
</template>
