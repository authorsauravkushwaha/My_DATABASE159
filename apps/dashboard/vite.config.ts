import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueJsx from '@vitejs/plugin-vue-jsx';
import mdx from '@mdx-js/rollup';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  return {
    plugins: [
      { enforce: 'pre', ...mdx({ jsxImportSource: 'vue', providerImportSource: '@mdx-js/vue' }) },
      vue(), vueJsx({ include: /\.(?:[jt]sx|mdx)$/ }),
    ],
    server: {
      host: '0.0.0.0', port: 5173, allowedHosts: ['.e2b.app'],
      proxy: { '/api': { target: env.API_PROXY_TARGET || 'http://127.0.0.1:8000', changeOrigin: true, rewrite: (path) => path.replace(/^\/api/, '') } },
    },
    preview: { host: '0.0.0.0', allowedHosts: ['.e2b.app'] },
    build: { target: 'es2022', sourcemap: false },
  };
});
