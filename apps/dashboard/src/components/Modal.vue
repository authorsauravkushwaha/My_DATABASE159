<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue';
import Icon from './Icon.vue';
const props = defineProps<{ title: string; wide?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>();
watch(() => props.title, async () => { await nextTick(); if (props.title && !dialog.value?.open) dialog.value?.showModal(); }, { immediate: true });
function close() { dialog.value?.close(); emit('close'); }
onBeforeUnmount(() => dialog.value?.close());
</script>
<template>
  <dialog ref="dialog" :class="['modal', { wide }]" :aria-label="title" @cancel.prevent="close" @click.self="close">
    <div class="modal-heading"><h2>{{ title }}</h2><button class="icon-button" aria-label="Close dialog" @click="close"><Icon name="close" /></button></div>
    <slot></slot>
  </dialog>
</template>
