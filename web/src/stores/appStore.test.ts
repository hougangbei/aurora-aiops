import { beforeEach, describe, expect, it, vi } from 'vitest';

describe('Aurora AIOps persisted state migration', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
  });

  it('copies legacy state once when the new storage key is absent', async () => {
    const legacy = JSON.stringify({
      state: { namespace: 'kube-system', dataMode: 'live' },
      version: 0,
    });
    localStorage.setItem('kubejojo-app', legacy);

    const { useAppStore } = await import('./appStore');

    expect(localStorage.getItem('aurora-aiops-app')).toBe(legacy);
    expect(useAppStore.getState().namespace).toBe('kube-system');
  });

  it('never overwrites an existing Aurora state with legacy state', async () => {
    localStorage.setItem(
      'aurora-aiops-app',
      JSON.stringify({ state: { namespace: 'default', dataMode: 'demo' }, version: 0 }),
    );
    localStorage.setItem(
      'kubejojo-app',
      JSON.stringify({ state: { namespace: 'kube-system', dataMode: 'live' }, version: 0 }),
    );

    const { useAppStore } = await import('./appStore');

    expect(useAppStore.getState().namespace).toBe('default');
  });
});
