<script setup lang="ts">
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { startDemo } from '../lib/store';
import Icon from '../components/Icon.vue';
import dashboardShot from '../assets/dashboard.png';

const router = useRouter();
const busy = ref(false);
const error = ref('');

const codeSample = `import { KestrelClient } from '@kestrel/client';

const db = new KestrelClient({ baseUrl: '/api/' });
const session = await db.login({ email, password });
db.setToken(session.access_token);

const [project] = await db.listProjects(
  session.organization_id!,
);

const records = await db.listRecords(
  project.id,
  'notes',
  { limit: 25, offset: 0 },
);`;

const sections = [
  { id: 'features', label: 'What works' },
  { id: 'architecture', label: 'Architecture' },
  { id: 'limits', label: 'Limits' },
];

function scrollTo(id: string) {
  const node = document.getElementById(id);
  if (node) node.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

async function openDemo() {
  busy.value = true;
  error.value = '';
  try {
    await startDemo();
    await router.push('/overview');
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The demo could not start. Please try again.';
  } finally {
    busy.value = false;
  }
}

const features = [
  {
    icon: 'shield',
    title: 'Tenant-aware from the first request',
    body: 'Organizations, membership checks, and short-lived bearer tokens guard every project, collection, and record.',
  },
  {
    icon: 'grid',
    title: 'Schemas you can see',
    body: 'Build a closed, typed JSON schema in the workspace. Writes are validated against it, with bounded pages and no unbounded scans.',
  },
  {
    icon: 'table',
    title: 'A workspace that respects your attention',
    body: 'Create projects, shape collections, add and edit records, search the current page, and export JSON — without leaving the tab.',
  },
  {
    icon: 'code',
    title: 'A dependency-free TypeScript client',
    body: 'Typed responses, abort signals, request timeouts, encoded path segments, and structured errors in a package with zero runtime dependencies.',
  },
  {
    icon: 'bolt',
    title: 'An honest Go storage engine',
    body: 'A small LSM engine with a write-ahead log, SSTables, compaction, and torn-tail recovery, fronted by an HTTP gateway that delegates authorization.',
  },
  {
    icon: 'book',
    title: 'Documentation in the product',
    body: 'Getting started, architecture, and security guides are written in MDX and compiled straight into the dashboard — they ship with the code.',
  },
];

const stack = [
  { name: 'Vue 3', role: 'dashboard' },
  { name: 'TypeScript', role: 'client + UI' },
  { name: 'FastAPI', role: 'control plane' },
  { name: 'PostgreSQL', role: 'records' },
  { name: 'Go', role: 'engine + gateway' },
  { name: 'MDX', role: 'in-app docs' },
];

const implemented = [
  'Membership and write-role enforcement',
  'Salted password hashing, short-lived tokens',
  'Closed schemas and bounded pagination',
  'Memory-only browser tokens and a strict CSP',
  'Parameterized queries throughout',
];

const missing = [
  'MFA and verified email recovery',
  'Refresh-token revocation and invitations',
  'Edge rate limiting and PostgreSQL RLS',
  'Audit logs, managed backups, restore drills',
];
</script>

<template>
  <div class="landing">
    <header class="landing-nav">
      <RouterLink to="/home" class="brand"><span class="brand-mark">K</span>kestrel<span class="brand-dot dark">.</span></RouterLink>
      <nav class="landing-links" aria-label="Site sections">
        <button v-for="section in sections" :key="section.id" @click="scrollTo(section.id)">{{ section.label }}</button>
        <RouterLink to="/docs">Docs</RouterLink>
      </nav>
      <div class="landing-actions">
        <a class="ghost-link" href="https://github.com/authorsauravkushwaha/My_DATABASE159" target="_blank" rel="noreferrer noopener">GitHub <Icon name="arrow" :size="14" /></a>
        <button class="button primary" :disabled="busy" @click="openDemo">{{ busy ? 'Opening…' : 'Open the demo' }} <Icon name="arrow" :size="15" /></button>
      </div>
    </header>

    <main>
      <section class="hero">
        <div class="hero-copy">
          <span class="eyebrow">OPEN SOURCE · MIT LICENSED · v0.2</span>
          <h1>A database workspace<br>you can actually<br><em>understand</em><span class="hero-dot">.</span></h1>
          <p class="hero-lead">Kestrel pairs a tenant-aware PostgreSQL control plane with a typed Vue workspace, a Go storage engine, and a dependency-free TypeScript client. Small enough to read. Real enough to build on.</p>
          <div class="hero-cta">
            <button class="button primary large" :disabled="busy" @click="openDemo">Explore the live demo <Icon name="arrow" :size="16" /></button>
            <RouterLink to="/docs" class="button secondary large"><Icon name="book" :size="16" /> Read the docs</RouterLink>
          </div>
          <p class="hero-note"><span class="status-dot"></span>No account, no card, no server. The demo runs in this tab and resets on refresh.</p>
          <p v-if="error" class="hero-error" role="alert">{{ error }}</p>
        </div>

        <div class="hero-art">
          <div class="shot-frame">
            <div class="shot-bar">
              <span class="shot-dots"><i></i><i></i><i></i></span>
              <span class="shot-url">kestrel.local / overview</span>
              <span class="shot-pill">demo mode</span>
            </div>
            <img :src="dashboardShot" alt="The Kestrel dashboard: sidebar navigation, workspace statistics, project list, and getting-started steps." width="1440" height="1042" />
          </div>
          <div class="float-card float-schema">
            <span class="float-icon"><Icon name="database" :size="16" /></span>
            <div><strong>products</strong><small>3 typed fields · closed schema</small></div>
          </div>
          <div class="float-card float-api">
            <span class="status-dot"></span>
            <div><strong>API ready</strong><small>project-scoped access</small></div>
          </div>
        </div>
      </section>

      <section class="stack-strip" aria-label="Technology">
        <span class="strip-label">BUILT WITH</span>
        <div class="strip-items">
          <span v-for="item in stack" :key="item.name" class="stack-item"><strong>{{ item.name }}</strong><small>{{ item.role }}</small></span>
        </div>
      </section>

      <section id="features" class="section features">
        <div class="section-head">
          <span class="eyebrow">WHAT ACTUALLY WORKS</span>
          <h2>Every feature has a job<span class="heading-dot">.</span></h2>
          <p>No placeholder screens. Each piece of the workspace talks to a real, tested surface — or says plainly that it doesn't.</p>
        </div>
        <div class="feature-grid">
          <article v-for="feature in features" :key="feature.title" class="feature-card">
            <span class="feature-icon"><Icon :name="feature.icon" :size="19" /></span>
            <h3>{{ feature.title }}</h3>
            <p>{{ feature.body }}</p>
          </article>
        </div>
      </section>

      <section id="architecture" class="section architecture">
        <div class="section-head narrow">
          <span class="eyebrow">THREE PLANES, ONE REPOSITORY</span>
          <h2>Clear boundaries, drawn on purpose<span class="heading-dot">.</span></h2>
          <p>The workspace, the control plane, and the storage plane are separate on purpose. Where they don't connect, the documentation says so.</p>
        </div>
        <div class="arch-layout">
          <div class="arch-diagram">
            <div class="layer">
              <span class="layer-tag">01 · BROWSER</span>
              <h3>Vue workspace</h3>
              <p>Hash-routed SPA, memory-only tokens, explicit demo mode.</p>
            </div>
            <div class="layer-arrow"><span></span><small>/api/ · bearer token</small></div>
            <div class="layer">
              <span class="layer-tag">02 · CONTROL PLANE</span>
              <h3>FastAPI + PostgreSQL</h3>
              <p>Organizations, memberships, typed JSON records, Alembic migrations.</p>
            </div>
            <div class="layer-arrow split"><span></span><small>/v1/data/ · delegated auth</small></div>
            <div class="layer">
              <span class="layer-tag">03 · STORAGE PLANE</span>
              <h3>Go gateway + LSM engine</h3>
              <p>WAL, SSTables, compaction, and a gateway that fails closed.</p>
            </div>
          </div>
          <div class="code-panel">
            <div class="code-head"><span>TypeScript</span><span class="code-file">client.ts</span></div>
            <pre class="code-body"><code>{{ codeSample }}</code></pre>
            <div class="code-foot"><Icon name="check" :size="15" />Typed responses, abort signals, structured errors.</div>
          </div>
        </div>
      </section>

      <section id="limits" class="section limits">
        <div class="section-head narrow">
          <span class="eyebrow">THE HONEST PART</span>
          <h2>What ships, and what doesn't<span class="heading-dot">.</span></h2>
          <p>Kestrel is a working MVP, not a production-ready replacement for a managed database platform. Read the boundaries before you put sensitive data inside.</p>
        </div>
        <div class="limits-grid">
          <div class="limits-card good">
            <h3><Icon name="check" :size="17" /> Implemented</h3>
            <ul><li v-for="item in implemented" :key="item">{{ item }}</li></ul>
          </div>
          <div class="limits-card warn">
            <h3><Icon name="bolt" :size="17" /> Still missing</h3>
            <ul><li v-for="item in missing" :key="item">{{ item }}</li></ul>
          </div>
        </div>
        <p class="limits-note">The dashboard and the Go gateway keep <strong>separate record stores</strong>. There is no replication, dual-write transaction, or automatic migration between them — and the docs say exactly that.</p>
      </section>

      <section class="cta-band">
        <div>
          <span class="eyebrow light">YOUR DATA. YOUR FOUNDATION.</span>
          <h2>Build something worth keeping<span class="cta-dot">.</span></h2>
          <p>Open the demo workspace, or clone the repository and run the whole stack with Docker Compose.</p>
        </div>
        <div class="cta-actions">
          <button class="button lime large" :disabled="busy" @click="openDemo">Open the demo <Icon name="arrow" :size="16" /></button>
          <a class="button outline large" href="https://github.com/authorsauravkushwaha/My_DATABASE159" target="_blank" rel="noreferrer noopener"><Icon name="code" :size="16" /> View the source</a>
        </div>
      </section>
    </main>

    <footer class="landing-footer">
      <div class="footer-brand">
        <RouterLink to="/home" class="brand light"><span class="brand-mark">K</span>kestrel<span class="brand-dot">.</span></RouterLink>
        <p>A small, open database platform.<br>A solid place to start.</p>
      </div>
      <div class="footer-cols">
        <div><h4>Product</h4><button @click="scrollTo('features')">What works</button><button @click="scrollTo('architecture')">Architecture</button><button @click="scrollTo('limits')">Limits</button></div>
        <div><h4>Read</h4><RouterLink to="/docs">Documentation</RouterLink><RouterLink to="/docs" @click="scrollTo('architecture')">Security &amp; limits</RouterLink><a href="https://github.com/authorsauravkushwaha/My_DATABASE159" target="_blank" rel="noreferrer noopener">GitHub</a></div>
        <div><h4>Project</h4><a href="https://github.com/authorsauravkushwaha/My_DATABASE159/blob/main/LICENSE" target="_blank" rel="noreferrer noopener">MIT license</a><a href="https://github.com/authorsauravkushwaha/My_DATABASE159/blob/main/CHANGELOG.md" target="_blank" rel="noreferrer noopener">Changelog</a><a href="https://github.com/authorsauravkushwaha/My_DATABASE159/blob/main/CONTRIBUTING.md" target="_blank" rel="noreferrer noopener">Contributing</a></div>
      </div>
      <div class="footer-base"><span>© 2026 Kestrel contributors</span><span>v0.3.0 <span class="footer-dot">·</span> Built on open foundations</span></div>
    </footer>
  </div>
</template>

<style scoped>
.landing{--ink:#1d3329;--paper:#f7f8f5;--line:#e3e8dd;background:var(--paper);color:var(--ink);min-height:100vh}
.landing-nav{position:sticky;top:0;z-index:40;display:flex;align-items:center;gap:26px;padding:15px 5vw;background:#f7f8f5ee;backdrop-filter:blur(12px);border-bottom:1px solid var(--line)}
.landing-nav .brand{font-size:21px;gap:8px;letter-spacing:-1px;color:var(--deep)}
.landing-nav .brand-mark{width:26px;height:28px;font-size:20px;border-radius:6px;transform:none;padding-right:2px}
.landing-nav .brand-dot{color:#a9c58b;margin-left:-7px}
.landing-links{display:flex;align-items:center;gap:4px;margin-left:auto}
.landing-links button,.landing-links a{border:0;background:none;padding:8px 12px;font-size:12px;color:#5c6d60;border-radius:6px;font-weight:500}
.landing-links button:hover,.landing-links a:hover{background:#ecefe6;color:var(--deep)}
.landing-actions{display:flex;align-items:center;gap:14px}
.ghost-link{display:inline-flex;align-items:center;gap:6px;font-size:12px;color:#4d6154}
.ghost-link:hover{color:var(--deep)}
.landing .button.large{padding:13px 19px;font-size:13px;border-radius:8px}
.hero{display:grid;grid-template-columns:1.02fr 1.08fr;gap:5vw;align-items:center;padding:78px 5vw 62px;max-width:1420px;margin:0 auto}
.hero-copy h1{font-size:clamp(38px,4.6vw,63px);line-height:1.03;letter-spacing:-2.6px;font-weight:600;color:#16342a}
.hero-copy h1 em{font-style:italic;font-weight:450;color:#3f7053}
.hero-dot{color:#a9c58b}
.hero-lead{font-size:15px;line-height:1.85;color:#68796c;max-width:480px;margin:24px 0 30px}
.hero-cta{display:flex;flex-wrap:wrap;gap:12px}
.hero-note{display:flex;align-items:center;gap:8px;font-size:11px;color:#8b978a;margin-top:20px}
.hero-error{margin-top:14px;font-size:12px;color:#a4543f;background:#fcf0ec;border:1px solid #efd5cc;border-radius:6px;padding:10px 12px}
.hero-art{position:relative}
.shot-frame{background:#fff;border:1px solid #dde4d5;border-radius:13px;box-shadow:0 34px 70px -30px #223c2c47,0 2px 5px #223c2c0f;overflow:hidden;transform:rotate(-.6deg)}
.shot-bar{display:flex;align-items:center;gap:12px;padding:10px 14px;border-bottom:1px solid #eef1e8;background:#fbfcf8}
.shot-dots{display:flex;gap:5px}
.shot-dots i{width:7px;height:7px;border-radius:50%;background:#dde4d3}
.shot-dots i:first-child{background:#e6c4b8}
.shot-dots i:nth-child(2){background:#e8dcc0}
.shot-url{font-size:9px;color:#9aa791;letter-spacing:.3px}
.shot-pill{margin-left:auto;font-size:8px;letter-spacing:.7px;text-transform:uppercase;color:#8a7a44;background:#f4efd9;padding:3px 7px;border-radius:4px}
.shot-frame img{display:block;width:100%;height:auto}
.float-card{position:absolute;display:flex;align-items:center;gap:10px;background:#fff;border:1px solid #e2e8d8;border-radius:9px;padding:11px 13px;box-shadow:0 16px 30px -16px #223c2c52;font-size:11px}
.float-card strong{display:block;font-size:11px;color:#2f4b38}
.float-card small{display:block;font-size:9px;color:#93a08c;margin-top:2px}
.float-schema{left:-22px;bottom:34px;transform:rotate(-1.4deg)}
.float-api{right:-14px;top:44px;transform:rotate(1.6deg)}
.float-icon{display:grid;place-items:center;width:28px;height:28px;border-radius:7px;background:#edf4e3;color:#5c8443}
.stack-strip{display:flex;align-items:center;gap:24px;flex-wrap:wrap;padding:20px 5vw;border-top:1px solid var(--line);border-bottom:1px solid var(--line);background:#fffdf9}
.strip-label{font-size:9px;letter-spacing:1.8px;color:#a3ae9c}
.strip-items{display:flex;flex-wrap:wrap;gap:26px}
.stack-item{display:flex;align-items:baseline;gap:7px}
.stack-item strong{font-size:12px;color:#3a5342}
.stack-item small{font-size:10px;color:#9aa691}
.section{padding:82px 5vw;max-width:1420px;margin:0 auto}
.section-head{max-width:600px;margin-bottom:42px}
.section-head.narrow{max-width:620px}
.section-head h2{font-size:clamp(27px,3vw,38px);line-height:1.15;letter-spacing:-1.4px;font-weight:600;color:#1b362a}
.section-head p{font-size:13.5px;line-height:1.85;color:#75847a;margin-top:16px}
.feature-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:18px}
.feature-card{background:#fff;border:1px solid var(--line);border-radius:11px;padding:24px 22px;transition:transform .18s,border-color .18s,box-shadow .18s}
.feature-card:hover{transform:translateY(-2px);border-color:#d5deca;box-shadow:0 18px 34px -26px #223c2c5c}
.feature-icon{display:grid;place-items:center;width:34px;height:34px;border-radius:8px;background:#eef4e5;color:#5c8443;margin-bottom:16px}
.feature-card h3{font-size:14px;font-weight:650;letter-spacing:-.3px;color:#22392c;line-height:1.45}
.feature-card p{font-size:12.5px;line-height:1.78;color:#77857a;margin-top:9px}
.architecture{background:#fffdf9;border-top:1px solid var(--line);border-bottom:1px solid var(--line);max-width:none}
.architecture>.section-head,.arch-layout{max-width:1310px;margin-left:auto;margin-right:auto}
.arch-layout{display:grid;grid-template-columns:1fr 1fr;gap:44px;align-items:center}
.arch-diagram{display:flex;flex-direction:column;gap:0}
.layer{background:#f2f6ea;border:1px solid #e0e8d5;border-radius:10px;padding:18px 20px}
.layer:nth-of-type(3){background:#edf2ff0}
.layer:last-of-type{background:#f6f3e6;border-color:#e9e2cc}
.layer-tag{font-size:8px;letter-spacing:1.6px;color:#93a487}
.layer h3{font-size:14px;font-weight:650;margin:7px 0 6px;color:#2b4433}
.layer p{font-size:11.5px;line-height:1.7;color:#7f8d80}
.layer-arrow{display:flex;align-items:center;gap:12px;padding:7px 0 7px 20px}
.layer-arrow span{width:1px;height:26px;background:#cbd8bf;position:relative}
.layer-arrow span:after{content:"";position:absolute;bottom:0;left:-3px;width:7px;height:7px;border-right:1px solid #cbd8bf;border-bottom:1px solid #cbd8bf;transform:rotate(45deg)}
.layer-arrow small{font-size:9px;color:#98a78f;letter-spacing:.2px}
.code-panel{background:#14322a;border:1px solid #24483b;border-radius:11px;overflow:hidden;box-shadow:0 24px 50px -30px #14322a6b}
.code-head{display:flex;justify-content:space-between;align-items:center;padding:12px 17px;border-bottom:1px solid #24483b;font-size:10px;color:#cfe0c1;letter-spacing:.3px}
.code-file{color:#7f9d8c;font-family:ui-monospace,monospace;font-size:9.5px}
.code-panel pre.code-body{margin:0;border:0;border-radius:0;background:transparent;color:#c9dcc2;padding:20px 17px;font-size:11.5px;line-height:1.85;overflow-x:auto;border:none}
.code-foot{display:flex;align-items:center;gap:8px;padding:12px 17px;border-top:1px solid #24483b;font-size:10px;color:#a7c19b}
.limits{max-width:1310px}
.limits-grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}
.limits-card{border-radius:11px;padding:22px 24px;border:1px solid #e0e7d6;background:#fff}
.limits-card.warn{background:#fdfaf2;border-color:#efe6ce}
.limits-card h3{display:flex;align-items:center;gap:9px;font-size:13px;font-weight:650;margin-bottom:14px;color:#33503a}
.limits-card.good h3 svg{color:#6d9a50}
.limits-card.warn h3{color:#8a7334}
.limits-card ul{list-style:none;margin:0;padding:0;display:grid;gap:9px}
.limits-card li{font-size:12px;color:#728076;line-height:1.6;padding-left:16px;position:relative}
.limits-card li:before{content:"";position:absolute;left:0;top:7px;width:5px;height:5px;border-radius:50%;background:#c3d3b0}
.limits-card.warn li:before{background:#ddc997}
.limits-note{font-size:11.5px;color:#8a968b;line-height:1.85;margin-top:20px;max-width:820px}
.limits-note strong{color:#5c6f5f}
.cta-band{background:var(--deep);color:#fff;padding:56px 5vw;display:flex;align-items:center;gap:36px;flex-wrap:wrap;justify-content:space-between}
.cta-band h2{font-size:clamp(26px,2.8vw,36px);font-weight:550;letter-spacing:-1.3px;line-height:1.15;color:#f2f7e8}
.cta-band p{font-size:12.5px;color:#9db39d;margin-top:13px;max-width:520px;line-height:1.8}
.eyebrow.light{color:#93b07f}
.cta-dot{color:var(--lime)}
.cta-actions{display:flex;gap:12px;flex-wrap:wrap}
.landing .button.outline{background:transparent;border-color:#4d6f5b;color:#e3edda}
.landing .button.outline:hover{background:#25493a}
.landing-footer{padding:52px 5vw 26px;background:#113028;color:#9fb3a1}
.footer-brand .brand{font-size:24px;margin-bottom:14px}
.footer-brand p{font-size:11.5px;line-height:1.9;color:#88a08c}
.footer-cols{display:flex;gap:64px;flex-wrap:wrap;margin:34px 0 30px}
.footer-cols h4{font-size:9px;letter-spacing:1.6px;color:#7b947f;margin:0 0 12px;font-weight:600}
.footer-cols div{display:flex;flex-direction:column;gap:9px;align-items:flex-start}
.footer-cols a,.footer-cols button{border:0;background:none;padding:0;font-size:11.5px;color:#b3c6b4}
.footer-cols a:hover,.footer-cols button:hover{color:#e6f0d6}
.footer-base{display:flex;justify-content:space-between;gap:16px;flex-wrap:wrap;border-top:1px solid #25453a;padding-top:20px;font-size:10px;color:#7f978a}
.footer-dot{color:#6c8a76}
@media(max-width:1080px){.hero{grid-template-columns:1fr;padding-bottom:44px}.hero-art{order:2;margin-top:8px}.float-schema{left:0}.arch-layout{grid-template-columns:1fr;gap:30px}.feature-grid{grid-template-columns:repeat(2,1fr)}.landing-links{display:none}}
@media(max-width:760px){.landing-nav{padding:12px 20px;gap:12px}.landing-actions .ghost-link{display:none}.hero{padding:44px 20px 38px;gap:30px}.hero-copy h1{letter-spacing:-1.6px}.hero-lead{font-size:13.5px}.section{padding:56px 20px}.feature-grid,.limits-grid{grid-template-columns:1fr}.float-card{display:none}.shot-frame{transform:none}.stack-strip{padding:16px 20px;gap:16px}.strip-items{gap:16px}.cta-band{padding:40px 20px;gap:22px}.landing-footer{padding:40px 20px 22px}.footer-cols{gap:34px}.landing .button.large{width:100%;justify-content:center}.hero-cta{flex-direction:column}.cta-actions{width:100%}.cta-actions .button{width:100%;justify-content:center}}
</style>
