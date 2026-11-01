import { describe, expect, it, beforeAll } from 'vitest';
import { createApp, h, nextTick } from 'vue';
import Modal from './Modal.vue';

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.open = true; };
  HTMLDialogElement.prototype.close = function () { this.open = false; };
});
describe('Modal', () => {
  it('has an accessible name and emits close', async () => {
    const host = document.createElement('div'); document.body.append(host);
    let closes = 0;
    const app = createApp({ render: () => h(Modal, { title: 'Create project', onClose: () => { closes++; } }, { default: () => h('p', 'Form content') }) });
    app.mount(host); await nextTick();
    const dialog = host.querySelector('dialog');
    expect(dialog?.getAttribute('aria-label')).toBe('Create project');
    expect(dialog?.open).toBe(true);
    host.querySelector<HTMLButtonElement>('button[aria-label="Close dialog"]')?.click();
    expect(closes).toBe(1);
    app.unmount(); host.remove();
  });
});
