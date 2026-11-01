import { createApp } from 'vue';
import { createRouter, createWebHashHistory } from 'vue-router';
import App from './App.vue';
import { state } from './lib/store';
import Overview from './views/Overview.vue';
import Projects from './views/Projects.vue';
import Collections from './views/Collections.vue';
import Records from './views/Records.vue';
import Settings from './views/Settings.vue';
import Docs from './views/Docs.vue';
import './style.css';

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/overview' },
    { path: '/overview', component: Overview, meta: { title: 'Workspace overview' } },
    { path: '/projects', component: Projects, meta: { title: 'Projects' } },
    { path: '/collections', component: Collections, meta: { title: 'Collections' } },
    { path: '/records', component: Records, meta: { title: 'Data explorer' } },
    { path: '/settings', component: Settings, meta: { title: 'Project settings' } },
    { path: '/docs', component: Docs, meta: { title: 'Documentation' } },
    { path: '/:pathMatch(.*)*', redirect: '/overview' },
  ],
});
router.beforeEach((to) => { if (!state.session && !['/docs', '/overview'].includes(to.path)) return '/overview'; });
createApp(App).use(router).mount('#app');
