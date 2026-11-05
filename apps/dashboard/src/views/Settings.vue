<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { ApiKeySummary, CreatedApiKey } from '@kestrel/client';
import { api, state, currentProject, loadProjects, notify, errorMessage } from '../lib/store';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
const busy = ref(false); const deleting = ref(false); const confirmation = ref(''); const error = ref(''); const copied = ref(false);
const keys = ref<ApiKeySummary[]>([]); const keysHidden = ref(false); const keysBusy = ref(false);
const creating = ref(false); const minted = ref<CreatedApiKey | null>(null); const revoking = ref<ApiKeySummary | null>(null);
const keyForm = ref({ name: '', role: 'reader' as 'reader' | 'writer', expiry: '0' }); const keyError = ref(''); const secretCopied = ref(false);
const snippet = computed(() => `import { KestrelClient } from '@kestrel/client';\n\nconst db = new KestrelClient({ baseUrl: '/api/' });\nconst session = await db.login({\n  email: 'you@example.com',\n  password: process.env.KESTREL_PASSWORD!,\n});\ndb.setToken(session.access_token);\n\n// Bulk-ingest compressed batches & query via secondary B-tree indexes.\nawait db.batchCreateRecords('${currentProject.value?.id ?? 'PROJECT_ID'}', 'products', [\n  { name: 'Sensor pack', price: 49, in_stock: true },\n]);\nconst result = await db.queryRecords('${currentProject.value?.id ?? 'PROJECT_ID'}', 'products', {\n  filters: [{ field: 'price', op: 'lte', value: 100 }],\n  limit: 100,\n});\n`);
async function loadKeys() {
  keys.value = []; keysHidden.value = false;
  if (!currentProject.value) return;
  try { keys.value = await api.value.listApiKeys(currentProject.value.id); }
  catch (e) {
    const status = (e as { status?: number }).status;
    if (status === 403 || status === 404) keysHidden.value = true; // viewers and non-admins don't see the panel
    else notify(errorMessage(e));
  }
}
watch(currentProject, () => { confirmation.value = ''; void loadKeys(); }, { immediate: true });
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
async function createKey() {
  if (!currentProject.value || !keyForm.value.name.trim()) return;
  keysBusy.value = true; keyError.value = '';
  try {
    const created = await api.value.createApiKey(currentProject.value.id, {
      name: keyForm.value.name.trim(),
      role: keyForm.value.role,
      expires_in_days: keyForm.value.expiry === '0' ? null : Number(keyForm.value.expiry),
    });
    creating.value = false; minted.value = created; secretCopied.value = false;
    keyForm.value = { name: '', role: 'reader', expiry: '0' };
    await loadKeys();
  } catch (e) { keyError.value = errorMessage(e); } finally { keysBusy.value = false; }
}
async function revokeKey() {
  if (!currentProject.value || !revoking.value) return;
  keysBusy.value = true; keyError.value = '';
  try {
    await api.value.revokeApiKey(currentProject.value.id, revoking.value.id);
    revoking.value = null; await loadKeys(); notify('API key revoked.');
  } catch (e) { keyError.value = errorMessage(e); } finally { keysBusy.value = false; }
}
async function copySecret() {
  if (!minted.value) return;
  try { await navigator.clipboard.writeText(minted.value.api_key); secretCopied.value = true; } catch { notify('Clipboard unavailable. Select and copy the secret below.'); }
}
function keyStatus(key: ApiKeySummary): string {
  if (key.revoked_at) return 'Revoked';
  if (key.expires_at && new Date(key.expires_at) <= new Date()) return 'Expired';
  return 'Active';
}
function when(value: string | null): string {
  if (!value) return 'Never';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}
</script>
<template>
  <div class="page-heading"><div><div class="eyebrow">THE DETAILS THAT KEEP YOU IN CONTROL</div><h1>Project settings<span class="heading-dot">.</span></h1><p>Manage access and connect your application.</p></div></div>
  <div v-if="!currentProject" class="panel empty-state"><h2>No project selected</h2><p>Create a project to manage its settings.</p><RouterLink to="/projects" class="button primary">Go to projects</RouterLink></div>
  <div v-else class="settings-layout">
    <section class="panel settings-panel"><div class="panel-heading"><div><h2>General</h2><p>The essentials for this project.</p></div><Icon name="settings" :size="20" /></div><dl class="settings-details"><div><dt>Project name</dt><dd>{{ currentProject.name }}</dd></div><div><dt>Slug</dt><dd><code>{{ currentProject.slug }}</code></dd></div><div><dt>Project ID</dt><dd><code>{{ currentProject.id }}</code></dd></div></dl><div class="setting-row"><div><strong>Data API</strong><p>Allow collection and record requests for this project.</p></div><button :class="['toggle', { on: currentProject.api_enabled }]" role="switch" :aria-checked="currentProject.api_enabled" aria-label="Enable project data API" :disabled="busy" @click="toggle"><span></span></button></div></section>
    <section class="panel api-keys-panel" v-if="!keysHidden">
      <div class="panel-heading"><div><h2>API keys</h2><p>Project-scoped credentials for servers and CI. The secret is shown once.</p></div><button class="button primary small-button" :disabled="keysBusy" @click="creating = true; keyError = ''"><Icon name="plus" :size="14" />New key</button></div>
      <div v-if="!keys.length" class="keys-empty muted">No API keys yet. Mint a reader or writer key so a service can use the data plane without a browser session.</div>
      <ul class="key-list" v-else>
        <li v-for="key in keys" :key="key.id">
          <div class="key-main">
            <div class="key-title"><strong>{{ key.name }}</strong><span class="pill">{{ key.role === 'writer' ? 'Writer' : 'Reader' }}</span><span class="pill neutral">{{ keyStatus(key) }}</span></div>
            <code class="key-prefix">klr_{{ key.key_prefix }}…</code>
            <span class="small">Created {{ when(key.created_at) }} · Last used {{ when(key.last_used_at) }}<template v-if="key.expires_at"> · Expires {{ when(key.expires_at) }}</template></span>
          </div>
          <button v-if="!key.revoked_at" class="button danger-outline small-button" :disabled="keysBusy" @click="revoking = key; keyError = ''">Revoke</button>
          <span v-else class="small muted">Revoked {{ when(key.revoked_at) }}</span>
        </li>
      </ul>
      <div class="connect-note" v-if="state.demo"><Icon name="shield" :size="16" /><span>Demo keys are generated locally for illustration; no server issues or accepts them.</span></div>
    </section>
    <section class="panel connect-panel"><div class="panel-heading"><div><h2>Connect your application</h2><p>A typed client. No extra runtime dependencies.</p></div><button class="icon-button bordered" aria-label="Copy client example" @click="copy"><Icon :name="copied ? 'check' : 'copy'" :size="17" /></button></div><pre>{{ snippet }}</pre><div class="connect-note"><Icon name="shield" :size="16" /><span>{{ state.demo ? 'Illustrative SDK example. Demo records are not available through the real API.' : 'Keep credentials in environment variables. Browser tokens are kept in memory, not local storage.' }}</span></div></section>
    <section class="panel danger-zone"><div><h2>Delete project</h2><p>Permanently delete this project, its collections, and its control-plane records.</p></div><button class="button danger-outline" @click="deleting = true; error = ''"><Icon name="trash" :size="16" />Delete project</button></section>
  </div>
  <Modal v-if="deleting && currentProject" title="Delete this project?" @close="deleting = false"><p>This permanently deletes <strong>{{ currentProject.name }}</strong>, including all collections and control-plane records. This cannot be undone.</p><form @submit.prevent="remove"><label>Type <strong>{{ currentProject.slug }}</strong> to confirm<input v-model="confirmation" autocomplete="off" /></label><p v-if="error" class="error-box" role="alert">{{ error }}</p><div class="modal-actions"><button type="button" class="button secondary" @click="deleting = false">Keep project</button><button class="button danger" :disabled="busy || confirmation !== currentProject.slug">Delete project</button></div></form></Modal>
  <Modal v-if="creating" title="Create an API key" @close="creating = false">
    <p>Keys are scoped to <strong>{{ currentProject?.name }}</strong> and capped at reader or writer access.</p>
    <form @submit.prevent="createKey">
      <label>Name<input v-model="keyForm.name" placeholder="Nightly ingest" maxlength="80" autocomplete="off" required /></label>
      <label>Access<select v-model="keyForm.role"><option value="reader">Reader — reads and indexed queries only</option><option value="writer">Writer — full data plane, no admin</option></select></label>
      <label>Expires<select v-model="keyForm.expiry"><option value="0">Never</option><option value="30">30 days</option><option value="90">90 days</option><option value="365">1 year</option></select></label>
      <p v-if="keyError" class="error-box" role="alert">{{ keyError }}</p>
      <div class="modal-actions"><button type="button" class="button secondary" @click="creating = false">Cancel</button><button class="button primary" :disabled="keysBusy || !keyForm.name.trim()">Create key</button></div>
    </form>
  </Modal>
  <Modal v-if="minted" title="Copy your API key now" @close="minted = null; secretCopied = false">
    <p>This is the only time the full secret is shown. Only its SHA-256 digest is stored — put it in a secret store before closing this dialog.</p>
    <div class="secret-box"><code>{{ minted.api_key }}</code><button class="icon-button bordered" :aria-label="secretCopied ? 'API key copied' : 'Copy API key'" @click="copySecret"><Icon :name="secretCopied ? 'check' : 'copy'" :size="16" /></button></div>
    <p v-if="state.demo" class="small muted">Demo only: this generated key authenticates nothing.</p>
    <div class="modal-actions"><button class="button primary" @click="minted = null; secretCopied = false">Done</button></div>
  </Modal>
  <Modal v-if="revoking" title="Revoke this API key?" @close="revoking = null">
    <p><strong>{{ revoking.name }}</strong> stops working immediately. Revocation cannot be undone — mint a new key if the integration needs access again.</p>
    <p v-if="keyError" class="error-box" role="alert">{{ keyError }}</p>
    <div class="modal-actions"><button type="button" class="button secondary" @click="revoking = null">Keep key</button><button class="button danger" :disabled="keysBusy" @click="revokeKey">Revoke key</button></div>
  </Modal>
</template>
