<script setup lang="ts">
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { client, signIn, errorMessage } from '../lib/store';
import Icon from './Icon.vue';
import { DemoApi } from '../lib/demo';
const router = useRouter();
const mode = ref<'login' | 'register'>('login');
const email = ref(''); const password = ref(''); const organization = ref('');
const busy = ref(false); const error = ref('');
async function submit() {
  busy.value = true; error.value = '';
  try {
    const session = mode.value === 'login'
      ? await client.login({ email: email.value, password: password.value })
      : await client.register({ email: email.value, password: password.value, organization_name: organization.value });
    await signIn(session); await router.push('/overview');
  } catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
async function demo() {
  busy.value = true;
  try { const api = new DemoApi(); await signIn(await api.login({ email: '', password: '' }), true); await router.push('/overview'); }
  catch (e) { error.value = errorMessage(e); } finally { busy.value = false; }
}
</script>
<template>
  <main class="auth-page">
    <section class="auth-story">
      <a class="brand light" href="#/docs"><span class="brand-mark">K</span>kestrel<span class="brand-dot">.</span></a>
      <div class="auth-story-content">
        <span class="eyebrow">YOUR DATA. YOUR FOUNDATION.</span><h1>Build something<br>worth keeping.</h1><p>A thoughtful workspace for your projects, collections, and the data that connects them.</p>
        <div class="architecture-art" aria-hidden="true"><div class="art-node"><Icon name="code" :size="26" /><span>Your application</span></div><div class="art-line"></div><div class="art-core"><span class="brand-mark">K</span><strong>Kestrel</strong><small>One workspace. Clear boundaries.</small></div><div class="art-line"></div><div class="art-node"><Icon name="database" :size="26" /><span>Your database</span></div></div>
      </div>
      <div class="auth-foot"><Icon name="shield" :size="17" /> Open source · Tenant-aware · Built to be understood</div>
    </section>
    <section class="auth-form-side">
      <RouterLink to="/docs" class="auth-docs">Explore the docs <Icon name="arrow" :size="16" /></RouterLink>
      <div class="auth-form-wrap">
        <span class="eyebrow">LET’S GET YOU SET UP</span><h2>{{ mode === 'login' ? 'Welcome back.' : 'A fresh start.' }}</h2><p class="muted">{{ mode === 'login' ? 'Sign in to your database workspace.' : 'Create your account and your first organization.' }}</p>
        <div class="segmented"><button :class="{ active: mode === 'login' }" @click="mode = 'login'; error = ''">Sign in</button><button :class="{ active: mode === 'register' }" @click="mode = 'register'; error = ''">Create account</button></div>
        <form @submit.prevent="submit">
          <label v-if="mode === 'register'">Organization name<input v-model="organization" required minlength="2" maxlength="120" autocomplete="organization" placeholder="Acme Studio" /></label>
          <label>Email address<input v-model="email" type="email" required autocomplete="email" placeholder="you@company.com" /></label>
          <label>Password<input v-model="password" type="password" required :minlength="mode === 'register' ? 12 : 1" maxlength="128" :autocomplete="mode === 'register' ? 'new-password' : 'current-password'" placeholder="At least 12 characters" /></label>
          <p v-if="mode === 'register'" class="field-hint">Use a unique password with at least 12 characters.</p>
          <p v-if="error" class="error-box" role="alert">{{ error }}</p>
          <button class="button primary full" :disabled="busy">{{ busy ? 'Connecting…' : mode === 'login' ? 'Sign in to workspace' : 'Create workspace' }} <Icon name="arrow" :size="17" /></button>
        </form>
        <div class="or"><span></span>or take a look around<span></span></div>
        <button class="button secondary full" :disabled="busy" @click="demo"><Icon name="grid" :size="17" /> Explore the interactive demo</button>
        <p class="auth-note">Demo data stays in this tab and resets on refresh.<br>No account or credit card required.</p>
      </div>
      <p class="auth-bottom">A small, open database platform. A solid place to start.</p>
    </section>
  </main>
</template>
