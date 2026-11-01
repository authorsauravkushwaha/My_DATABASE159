import { computed, reactive, shallowRef } from 'vue';
import { KestrelClient, type AuthSession, type Collection, type Project } from '@kestrel/client';
import { DemoApi, type WorkspaceApi } from './demo';

export const client = new KestrelClient();
export const state = reactive({
  session: null as AuthSession | null,
  demo: false,
  projects: [] as Project[],
  projectId: '',
  collections: [] as Collection[],
  loading: false,
  error: '',
  notice: '',
  health: 'unknown' as 'unknown' | 'ready' | 'unavailable',
});
export const api = shallowRef<WorkspaceApi>(client);
export const currentProject = computed(() => state.projects.find((p) => p.id === state.projectId));
let noticeTimer: ReturnType<typeof setTimeout>;
let expiryTimer: ReturnType<typeof setTimeout>;
let loadVersion = 0;

export function notify(message: string) {
  state.notice = message; clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => { state.notice = ''; }, 4500);
}
export function errorMessage(error: unknown) { return error instanceof Error ? error.message : 'Something went wrong. Please try again.'; }
export function signOut() {
  client.clearToken(); state.session = null; state.demo = false; state.projects = []; state.collections = [];
  state.projectId = ''; state.error = ''; state.notice = ''; state.health = 'unknown'; api.value = client;
  clearTimeout(expiryTimer); loadVersion++; state.loading = false;
}
export async function signIn(session: AuthSession, demo = false) {
  signOut(); state.session = session; state.demo = demo;
  if (demo) api.value = new DemoApi(); else client.setToken(session.access_token);
  if (!demo) expiryTimer = setTimeout(() => { signOut(); state.error = 'Your session expired. Sign in again to continue.'; }, session.expires_in * 1000);
  await loadProjects();
}
export async function startDemo() {
  const demo = new DemoApi();
  await signIn(await demo.login({ email: '', password: '' }), true);
}
export async function loadProjects() {
  if (!state.session?.organization_id) return;
  const version = ++loadVersion; state.loading = true; state.error = '';
  try {
    const projects = await api.value.listProjects(state.session.organization_id);
    if (version !== loadVersion) return;
    state.projects = projects;
    if (!projects.some((p) => p.id === state.projectId)) state.projectId = projects[0]?.id ?? '';
    await loadCollections(version);
  } catch (error) { if (version === loadVersion) state.error = errorMessage(error); }
  finally { if (version === loadVersion) state.loading = false; }
}
export async function loadCollections(version = loadVersion) {
  if (!state.projectId || !currentProject.value?.api_enabled) { state.collections = []; return; }
  const projectId = state.projectId;
  try {
    const collections = await api.value.listCollections(projectId);
    if (version === loadVersion && projectId === state.projectId) state.collections = collections;
  } catch (error) { if (version === loadVersion) { state.collections = []; throw error; } }
}
export async function selectProject(id: string) {
  state.projectId = id; state.collections = []; await loadProjects();
}
export async function checkHealth() {
  if (state.demo) { state.health = 'unknown'; return; }
  try { await client.ready(); state.health = 'ready'; } catch { state.health = 'unavailable'; }
}
