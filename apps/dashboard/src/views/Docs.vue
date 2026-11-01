<script setup lang="ts">
import { ref } from 'vue';
import { state } from '../lib/store';
import { MDXProvider } from '@mdx-js/vue';
import GettingStarted from '../content/getting-started.mdx';
import Architecture from '../content/architecture.mdx';
import Security from '../content/security.mdx';
import Icon from '../components/Icon.vue';
const page = ref('start');
const pages = [{ id: 'start', title: 'Getting started', component: GettingStarted }, { id: 'architecture', title: 'Architecture', component: Architecture }, { id: 'security', title: 'Security & limits', component: Security }];
const components = { a: 'a', pre: 'pre', code: 'code' };
</script>
<template>
  <div class="page-heading"><div><div class="eyebrow">OPEN BOOKS. SOLID FOUNDATIONS.</div><h1>Build with understanding<span class="heading-dot">.</span></h1><p>The practical guide to your database workspace.</p></div><span class="pill docs-version">v0.2 documentation</span></div>
  <div class="docs-layouthead"><nav class="docs-tabs" aria-label="Documentation topics"><button v-for="item in pages" :key="item.id" :class="{ active: page === item.id }" @click="page = item.id">{{ item.title }}</button></nav><RouterLink v-if="!state.session" to="/overview" class="text-link">Open workspace <Icon name="arrow" :size="16" /></RouterLink></div>
  <article class="panel docs-article"><MDXProvider :components="components"><component :is="pages.find(p => p.id === page)?.component" /></MDXProvider></article>
</template>
