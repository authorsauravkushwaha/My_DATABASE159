<script setup lang="ts">
withDefaults(defineProps<{ name: string; size?: number }>(), { size: 20 });
const paths: Record<string, string> = {
  grid: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
  database: 'M20 6c0 2.2-3.6 4-8 4S4 8.2 4 6s3.6-4 8-4 8 1.8 8 4Z M4 6v12c0 2.2 3.6 4 8 4s8-1.8 8-4V6 M4 12c0 2.2 3.6 4 8 4s8-1.8 8-4',
  folder: 'M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z',
  table: 'M3 4h18v16H3z M3 9h18 M3 14h18 M9 9v11',
  settings: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1Z',
  book: 'M12 5v16 M3 3h5a4 4 0 0 1 4 2 4 4 0 0 1 4-2h5v16h-5a4 4 0 0 0-4 2 4 4 0 0 0-4-2H3Z',
  arrow: 'M5 12h14 M13 6l6 6-6 6',
  plus: 'M12 5v14 M5 12h14',
  search: 'M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14 M15 15l6 6',
  check: 'M5 12l4 4L19 6',
  close: 'M6 6l12 12 M6 18 18 6',
  copy: 'M8 8h12v13H8z M16 8V3H3v13h5',
  download: 'M12 3v12 M7 10l5 5 5-5 M4 16v5h16v-5',
  upload: 'M12 15V3 M7 8l5-5 5 5 M4 16v5h16v-5',
  filter: 'M3 5h18l-7 8v6l-4-2v-4Z',
  layers: 'M12 3 2 8l10 5 10-5-10-5Z M2 13l10 5 10-5 M2 18l10 5 10-5',
  edit: 'm16 3 5 5-12 12H4v-5Z M13 6l5 5',
  trash: 'M3 6h18 M9 6V3h6v3 M5 6l1 15h12l1-15 M10 10v7 M14 10v7',
  shield: 'M12 3 3 7v5c0 5 9 10 9 10s9-5 9-10V7Z M8 12l3 3 5-6',
  bolt: 'm13 2-9 12h7l-1 8 10-13h-7Z',
  logout: 'M9 4H3v16h6 M9 12h12 M16 7l5 5-5 5',
  menu: 'M4 6h16 M4 12h16 M4 18h16',
  code: 'm8 6-6 6 6 6 M16 6l6 6-6 6 M14 3l-4 18',
};
</script>
<template>
  <svg :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name] ?? paths.database" /></svg>
</template>
