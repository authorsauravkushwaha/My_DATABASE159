/// <reference types="vite/client" />
declare module '*.mdx' {
  import type { DefineComponent } from 'vue';
  const component: DefineComponent;
  export default component;
}
