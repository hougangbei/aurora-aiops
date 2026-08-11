import { beforeEach, describe, expect, it, vi } from 'vitest';

const getMock = vi.fn();
const postMock = vi.fn();

vi.mock('../../services/http', () => ({
  http: {
    get: (...args: unknown[]) => getMock(...args),
    post: (...args: unknown[]) => postMock(...args),
  },
}));

import {
  collectAssetServer,
  confirmAssetHostKey,
  createAssetServer,
  getAssetServer,
  getLatestAssetSnapshot,
  listAssetServers,
  listAssetSoftware,
  testAssetConnection,
} from './api';

function okEnvelope<T>(data: T) {
  return { data: { code: 'OK', message: 'success', data } };
}

describe('asset API client', () => {
  beforeEach(() => {
    getMock.mockReset();
    postMock.mockReset();
  });

  it('lists asset servers from the response envelope', async () => {
    getMock.mockResolvedValue(okEnvelope([{ id: 'server-1' }]));

    await expect(listAssetServers()).resolves.toEqual([{ id: 'server-1' }]);
    expect(getMock).toHaveBeenCalledWith('/assets/servers');
  });

  it('creates an asset server with credential input only in the request body', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'server-1', credentialConfigured: true }));
    const input = {
      name: 'edge-1', address: '192.0.2.10', username: 'root', sshPort: 22,
      credentialAuthType: 'password' as const, password: 'test-password', testConnection: false,
    };

    await expect(createAssetServer(input)).resolves.toEqual({ id: 'server-1', credentialConfigured: true });
    expect(postMock).toHaveBeenCalledWith('/assets/servers', input);
  });

  it('gets a single server with an encoded id', async () => {
    getMock.mockResolvedValue(okEnvelope({ id: 'server-1' }));

    await getAssetServer('server/with?chars');
    expect(getMock).toHaveBeenCalledWith('/assets/servers/server%2Fwith%3Fchars');
  });

  it('posts connection, host-key confirmation, and collection actions', async () => {
    postMock.mockResolvedValue(okEnvelope({ fingerprint: 'SHA256:test', trusted: true, changed: false }));

    await testAssetConnection('server-1');
    await expect(confirmAssetHostKey('server-1', 'SHA256:test')).resolves.toBeUndefined();
    await collectAssetServer('server-1');

    expect(postMock).toHaveBeenNthCalledWith(1, '/assets/servers/server-1/test-connection');
    expect(postMock).toHaveBeenNthCalledWith(2, '/assets/servers/server-1/confirm-host-key', {
      fingerprint: 'SHA256:test',
    });
    expect(postMock).toHaveBeenNthCalledWith(3, '/assets/servers/server-1/collect');
  });

  it('gets the latest snapshot and software list with encoded ids', async () => {
    getMock.mockResolvedValueOnce(okEnvelope({ id: 'snapshot-1' })).mockResolvedValueOnce(okEnvelope([]));

    await expect(getLatestAssetSnapshot('server/a')).resolves.toEqual({ id: 'snapshot-1' });
    await expect(listAssetSoftware('server/a')).resolves.toEqual([]);

    expect(getMock).toHaveBeenNthCalledWith(1, '/assets/servers/server%2Fa/snapshots/latest');
    expect(getMock).toHaveBeenNthCalledWith(2, '/assets/servers/server%2Fa/software');
  });
});
