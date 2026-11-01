<script setup lang="ts">
import { ref } from 'vue';
import { api, state, loadProjects, selectProject, notify, errorMessage } from '../lib/store';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
const open = ref(false); const name = ref(''); const slug = ref(''); const busy = ref(false); const error = ref(''); const search = ref('');
function editSlug() { slug.value = name.value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 80); }
async function create() {
  if (!state.session?.organization_id) return;
  busy.value = true; error.value = '';
  try { const project = await api.value.createProject(state.session.organization_id, { name: name.value.trim(), slug: slug.value }); await loadProjects(); await selectProject(project.id); open.value = false; name.value = ''; slug.value = ''; notify('Project created. Make it your own.'); }
  catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
</script>
<template>
  <div class="page-heading"><div><div class="eyebrow">ORGANIZE. ISOLATE. BUILD.</div><h1>Projects<span class="heading-dot">.</span></h1><p>Separate spaces for your applications, with boundaries that matter.</p></div><button class="button primary" @click="open = true"><Icon name="plus" :size="17" />New project</button></div>
  <div class="toolbar"><div class="search-input"><Icon name="search" :size="17" /><input v-model="search" placeholder="Find a project…" aria-label="Search projects" /></div><span class="muted small">{{ state.projects.length }} project{{ state.projects.length === 1 ? '' : 's' }}</span></div>
  <div class="project-grid"><article v-for="project in state.projects.filter(p => `${p.name} ${p.slug}`.toLowerCase().includes(search.toLowerCase()))" :key="project.id" class="project-card" :class="{ selected: project.id === state.projectId }"><div class="project-card-top"><span class="project-icon large"><Icon name="database" :size="25" /></span><span v-if="project.id === state.projectId" class="pill">Active project</span></div><h2>{{ project.name }}</h2><code>{{ project.slug }}</code><div class="project-meta"><span><span :class="['status-dot', !project.api_enabled && 'gray']"></span>Data API {{ project.api_enabled ? 'enabled' : 'disabled' }}</span><span>{{ new Date(project.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) }}</span></div><button class="button secondary full" @click="selectProject(project.id)">{{ project.id === state.projectId ? 'Manage collections' : 'Switch to project' }}<Icon name="arrow" :size="16" /></button></article><button class="new-project-card" @click="open = true"><span><Icon name="plus" :size="25" /></span><strong>A new beginning</strong><p>Create a project for your next idea.</p></button></div>
  <div v-if="search && !state.projects.some(p => `${p.name} ${p.slug}`.toLowerCase().includes(search.toLowerCase()))" class="empty-small">No projects match “{{ search }}”.</div>
  <div class="info-strip"><Icon name="shield" :size="20" /><div><strong>Separate by design.</strong><span> Collections and records are scoped to their project. Organization membership is checked on every control-plane request.</span></div></div>
  <Modal v-if="open" title="Create a project" @close="open = false"><p class="muted">Give your next idea a space of its own.</p><form @submit.prevent="create"><label>Project name<input v-model="name" required minlength="2" maxlength="120" placeholder="My application" @input="editSlug" /></label><label>Project slug<input v-model="slug" required minlength="2" maxlength="80" pattern="[a-z0-9]+(?:-[a-z0-9]+)*" placeholder="my-application" /><span class="field-hint">Lowercase letters, numbers, and hyphens.</span></label><p v-if="error" class="error-box" role="alert">{{ error }}</p><div class="modal-actions"><button type="button" class="button secondary" @click="open = false">Cancel</button><button class="button primary" :disabled="busy">{{ busy ? 'Creating…' : 'Create project' }}</button></div></form></Modal>
</template>
