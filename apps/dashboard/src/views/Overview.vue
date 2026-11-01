<script setup lang="ts">
import { computed } from 'vue';
import { state, currentProject, selectProject } from '../lib/store';
import { formatBytes } from '../lib/schema';
import Icon from '../components/Icon.vue';
const fieldCount = computed(() => state.collections.reduce((sum, item) => sum + Object.keys(item.schema.properties).length, 0));
const storage = computed(() => state.storage);
</script>
<template>
  <div class="page-heading">
    <div><div class="eyebrow">WELCOME BACK</div><h1>Your workspace, at a glance<span class="heading-dot">.</span></h1><p>Projects, collections, compressed storage density, and active data metrics in one calm view.</p></div>
    <RouterLink to="/projects" class="button primary"><Icon name="plus" :size="16" />New project</RouterLink>
  </div>
  <section class="welcome-panel">
    <div>
      <span class="pill light-pill"><span class="status-dot"></span>HIGH-DENSITY WORKSPACE READY</span>
      <h2>Your data.<br>Built to scale.</h2>
      <p>Kestrel pairs compressed record storage, secondary B-tree field indexes, sharded LSM SSTables, and streaming NDJSON ingestion so datasets stay compact and fast.</p>
      <RouterLink to="/collections" class="button lime">Explore collections <Icon name="arrow" :size="15" /></RouterLink>
    </div>
    <div class="welcome-visual" aria-hidden="true">
      <div class="orbit"></div><div class="orbit orbit-two"></div>
      <div class="data-card data-card-back"><span></span><span></span><span></span></div>
      <div class="data-card data-card-front">
        <div><Icon name="database" :size="17" /><strong>{{ state.collections[0]?.name ?? 'collections' }}</strong><span class="card-dot"></span></div>
        <div v-for="n in 3" :key="n" class="mini-row"><i class="mini-cell"></i><i class="mini-cell"></i><i class="mini-cell"></i></div>
        <footer><span class="status-dot"></span>Compressed · {{ storage ? `${storage.compression_ratio}×` : 'ready' }}</footer>
      </div>
      <span class="visual-spark">✦</span>
    </div>
  </section>
  <section class="stats-grid" aria-label="Workspace summary">
    <article class="stat-card"><div class="stat-label"><span>Projects</span><Icon name="folder" :size="18" /></div><strong>{{ state.projects.length }}</strong><small><span class="status-dot"></span>Scoped to your organization</small></article>
    <article class="stat-card"><div class="stat-label"><span>Collections</span><Icon name="grid" :size="18" /></div><strong>{{ state.collections.length }}</strong><small>{{ currentProject ? `In ${currentProject.name}` : 'Create or select a project' }}</small></article>
    <article class="stat-card"><div class="stat-label"><span>Records stored</span><Icon name="table" :size="18" /></div><strong>{{ storage ? storage.total_records.toLocaleString() : '0' }}</strong><small>{{ storage ? `${storage.indexed_fields_count} B-tree field indexes · ${fieldCount} fields` : `${fieldCount} validated fields` }}</small></article>
    <article class="stat-card"><div class="stat-label"><span>Storage density</span><Icon name="layers" :size="18" /></div><strong class="stat-text">{{ storage && storage.total_records > 0 ? `${storage.compression_ratio}×` : (currentProject?.api_enabled ? 'Ready' : 'Paused') }}</strong><small><span :class="['status-dot', currentProject?.api_enabled ? '' : 'gray']"></span>{{ storage && storage.total_records > 0 ? `${formatBytes(storage.stored_bytes)} stored (${formatBytes(storage.saved_bytes)} saved)` : (state.demo ? 'Local demo adapter' : currentProject?.api_enabled ? 'KZ1 + KSS2 compression active' : 'API disabled in settings') }}</small></article>
  </section>
  <section v-if="storage" class="capacity-strip" aria-label="Storage engine telemetry">
    <div class="capacity-metric">
      <span class="capacity-label">LOGICAL PAYLOAD</span>
      <strong>{{ formatBytes(storage.raw_bytes) }}</strong>
      <small>{{ storage.compressed_records.toLocaleString() }} compressed records</small>
    </div>
    <div class="capacity-metric">
      <span class="capacity-label">STORED FOOTPRINT</span>
      <strong>{{ formatBytes(storage.stored_bytes) }}</strong>
      <small>{{ storage.compression_ratio }}× effective density</small>
    </div>
    <div class="capacity-metric">
      <span class="capacity-label">HASH SHARDS</span>
      <strong>{{ storage.active_shards }} / {{ storage.virtual_shards }}</strong>
      <small>Deterministic partition buckets</small>
    </div>
    <div class="capacity-metric">
      <span class="capacity-label">LSM STORAGE ENGINE</span>
      <strong>{{ storage.engine ? `${storage.engine.shards ?? 1} shards · ${storage.engine.sstables ?? 0} SSTs` : (state.demo ? 'In-memory demo' : 'Control plane') }}</strong>
      <small>{{ storage.engine ? `Bloom + LRU cache · ${storage.engine.compression_ratio ?? 1}× SST ratio` : 'Batch ingest & streaming NDJSON enabled' }}</small>
    </div>
  </section>
  <section class="overview-bottom">
    <div class="panel">
      <div class="panel-heading"><div><h2>Recent projects</h2><p>Jump back into active workspaces.</p></div><RouterLink to="/projects" class="text-link">View all <Icon name="arrow" :size="14" /></RouterLink></div>
      <div v-if="!state.projects.length" class="empty-small">No projects yet. Create one to start defining collections and bulk-ingesting datasets.</div>
      <RouterLink v-for="project in state.projects.slice(0, 4)" :key="project.id" to="/collections" class="project-row" @click="selectProject(project.id)">
        <span class="project-icon"><Icon name="database" :size="18" /></span>
        <div><strong>{{ project.name }}</strong><small>{{ project.slug }}</small></div>
        <span :class="['pill', project.api_enabled ? '' : 'neutral']"><span :class="['status-dot', project.api_enabled ? '' : 'gray']"></span>{{ project.api_enabled ? 'Active' : 'API off' }}</span>
        <Icon name="arrow" :size="16" />
      </RouterLink>
    </div>
    <div class="panel">
      <div class="panel-heading"><div><h2>Getting started</h2><p>Four steps to high-capacity data storage.</p></div></div>
      <RouterLink to="/projects" class="start-row"><span class="step-number">01</span><div><strong>Create a project</strong><small>Isolate schemas, hash shards, and API access per app.</small></div><Icon name="arrow" :size="15" /></RouterLink>
      <RouterLink to="/collections" class="start-row"><span class="step-number">02</span><div><strong>Shape schema &amp; B-tree indexes</strong><small>Up to 128 typed fields and 16 secondary indexes per collection.</small></div><Icon name="arrow" :size="15" /></RouterLink>
      <RouterLink to="/records" class="start-row"><span class="step-number">03</span><div><strong>Bulk ingest &amp; query with cursors</strong><small>Batch-write up to 5,000 records/call and stream NDJSON exports.</small></div><Icon name="arrow" :size="15" /></RouterLink>
    </div>
  </section>
</template>
