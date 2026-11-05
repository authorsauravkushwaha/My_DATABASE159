import { describe, expect, it } from 'vitest';
import { DemoApi } from './demo';

describe('DemoApi project API keys', () => {
  it('mints one-time secrets, strips them from listings, and revokes idempotently', async () => {
    const demo = new DemoApi();
    const project = (await demo.listProjects('demo-org'))[0]!.id;

    const key = await demo.createApiKey(project, { name: 'Demo writer', role: 'writer' });
    expect(key.api_key).toMatch(/^klr_[0-9a-f]{16}_[0-9a-f]{32}$/);
    expect(key.revoked_at).toBeNull();

    const listed = await demo.listApiKeys(project);
    expect(listed).toHaveLength(1);
    expect(listed[0]).not.toHaveProperty('api_key');
    expect(listed[0]!.role).toBe('writer');
    expect(listed[0]!.key_prefix).toHaveLength(16);

    await demo.revokeApiKey(project, key.id);
    await demo.revokeApiKey(project, key.id); // idempotent, like the real DELETE
    const after = await demo.listApiKeys(project);
    expect(after[0]!.revoked_at).toBeTruthy();

    await expect(demo.revokeApiKey(project, 'missing-key')).rejects.toMatchObject({ status: 404 });
    await expect(demo.createApiKey('unknown-project', { name: 'x' })).rejects.toMatchObject({ status: 404 });
  });

  it('honors expiry input and defaults to reader scope', async () => {
    const demo = new DemoApi();
    const project = (await demo.listProjects('demo-org'))[0]!.id;
    const key = await demo.createApiKey(project, { name: 'Ephemeral', expires_in_days: 7 });
    expect(key.role).toBe('reader');
    expect(key.expires_at).toBeTruthy();
    expect(new Date(key.expires_at!).getTime()).toBeGreaterThan(Date.now());
  });
});
