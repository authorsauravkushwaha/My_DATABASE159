<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { state, selectProject, signOut, checkHealth, loadProjects } from './lib/store';
import Icon from './components/Icon.vue';
import Auth from './components/Auth.vue';
const route = useRoute(); const router = useRouter(); const mobileOpen = ref(false);
const navigation = [
  { path: '/overview', label: 'Overview', icon: 'grid' },
  { path: '/projects', label: 'Projects', icon: 'folder' },
  { path: '/collections', label: 'Collections', icon: 'database' },
  { path: '/records', label: 'Data explorer', icon: 'table' },
];
watch(() => route.path, () => { mobileOpen.value = false; });
onMounted(checkHealth);
async function logout() { signOut(); await router.push('/overview'); }
</script>
<template>
  <Auth v-if="!state.session && route.path !== '/docs'" />
  <div v-else class="app-shell">
    <a href="#main-content" class="skip-link">Skip to content</a>
    <button v-if="mobileOpen" class="sidebar-backdrop" aria-label="Close navigation" @click="mobileOpen = false"></button>
    <aside class="sidebar" :class="{ open: mobileOpen }">
      <RouterLink to="/overview" class="brand light"><span class="brand-mark">K</span>kestrel<span class="brand-dot">.</span></RouterLink>
      <div class="workspace-label"><span class="workspace-avatar">{{ state.demo ? 'D' : 'W' }}</span><div><strong>{{ state.demo ? 'Demo workspace' : 'My workspace' }}</strong><small>{{ state.demo ? 'In-memory sandbox' : 'Developer workspace' }}</small></div></div>
      <template v-if="state.session">
        <label class="project-switch">PROJECT<select :value="state.projectId" aria-label="Active project" @change="selectProject(($event.target as HTMLSelectElement).value)"><option v-if="!state.projects.length" value="">No projects yet</option><option v-for="project in state.projects" :key="project.id" :value="project.id">{{ project.name }}</option></select></label>
        <div class="nav-caption">WORKSPACE</div>
        <nav aria-label="Main navigation"><RouterLink v-for="item in navigation" :key="item.path" :to="item.path" :class="{ active: route.path === item.path }"><Icon :name="item.icon" :size="19" />{{ item.label }}<span v-if="item.path === 'projects'" class="nav-count">{{ state.projects.length }}</span></RouterLink></nav>
        <div class="nav-caption second">MANAGE</div>
        <nav aria-label="Project management"><RouterLink to="/settings" :class="{ active: route.path === '/settings' }"><Icon name="settings" :size="19" />Project settings</RouterLink></nav>
      </template>
      <div class="sidebar-bottom"><RouterLink to="/docs" class="docs-link" :class="{ active: route.path === '/docs' }"><Icon name="book" :size="19" />Documentation<Icon name="arrow" :size="15" /></RouterLink><div class="sidebar-tip"><span class="tiny-spark">✦</span><strong>Small by design.</strong><p>Understand your stack.<br>Own your foundation.</p><RouterLink to="/docs">Meet the architecture <span>→</span></RouterLink></div><button v-if="state.session" class="profile" @click="logout"><span class="avatar">{{ state.demo ? 'D' : 'U' }}</span><span><strong>{{ state.demo ? 'Demo explorer' : 'Workspace member' }}</strong><small>Sign out</small></span><Icon name="logout" :size="17" /></button><span v-else class="version">Kestrel v0.2 · MIT licensed</span></div>
    </aside>
    <div class="main-shell">
      <header class="topbar"><div class="breadcrumb"><button class="icon-button mobile-menu" aria-label="Open navigation" @click="mobileOpen = true"><Icon name="menu" /></button><span>Workspace</span><span class="slash">/</span><strong>{{ route.meta.title ?? 'Documentation' }}</strong></div><div class="topbar-right"><span class="environment"><span :class="['status-dot', state.demo ? 'amber' : state.health === 'ready' ? '' : 'gray']"></span>{{ state.demo ? 'Demo mode' : state.health === 'ready' ? 'API ready' : state.health === 'unavailable' ? 'API unavailable' : 'Control plane' }}</span><span class="topbar-divider"></span><RouterLink to="/docs" class="help-link">Help & docs <Icon name="arrow" :size="14" /></RouterLink></div></header>
      <div v-if="state.demo" class="demo-banner"><Icon name="bolt" :size="15" /><span>You’re exploring a local demo. Changes are not saved to a server.</span><button @click="logout">Exit demo <Icon name="close" :size="13" /></button></div>
      <main id="main-content" class="main-content">
        <div v-if="state.error" class="error-box page-error" role="alert"><span>{{ state.error }}</span><button class="text-button" @click="loadProjects">Retry</button></div>
        <div v-if="state.loading" class="loading-line" role="progressbar" aria-label="Loading workspace"></div>
        <RouterView />
        <footer class="page-footer"><span>Built on open foundations.</span><span>Kestrel <span class="footer-dot">·</span> v0.2.0</span></footer>
      </main>
    </div>
    <div v-if="state.notice" class="toast" role="status"><span class="toast-check"><Icon name="check" :size="16" /></span>{{ state.notice }}<button class="icon-button" aria-label="Dismiss notification" @click="state.notice = ''"><Icon name="close" :size="15" /></button></div>
  </div>
</template>
