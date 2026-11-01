/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Asset base path; set to /<repo>/ for GitHub Pages project sites. */
  readonly VITE_BASE?: string;
  /** 'true' on the static GitHub Pages build, which has no control plane. */
  readonly VITE_STATIC_DEMO?: string;
}
interface ImportMeta { readonly env: ImportMetaEnv }
declare module '*.mdx' {
  import type { DefineComponent } from 'vue';
  const component: DefineComponent;
  export default component;
}
