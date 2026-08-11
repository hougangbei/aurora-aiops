import { beforeEach, describe, expect, it, vi } from 'vitest';

const getMock = vi.fn();
const postMock = vi.fn();

vi.mock('../../services/http', () => ({
  http: {
    get: (...args: unknown[]) => getMock(...args),
    post: (...args: unknown[]) => postMock(...args),
  },
}));

import { getProject, installProject, listProjects } from './api';

function okEnvelope<T>(data: T) {
  return { data: { code: 'OK', data } };
}

describe('project API client', () => {
  beforeEach(() => {
    getMock.mockReset();
    postMock.mockReset();
  });

  it('lists projects from the exact project-center endpoint', async () => {
    getMock.mockResolvedValue(okEnvelope([{ id: 'aurora-aiops' }]));
    await expect(listProjects()).resolves.toEqual([{ id: 'aurora-aiops' }]);
    expect(getMock).toHaveBeenCalledWith('/projects');
  });

  it('gets an encoded project detail', async () => {
    getMock.mockResolvedValue(okEnvelope({ id: 'aurora/aiops' }));
    await getProject('aurora/aiops');
    expect(getMock).toHaveBeenCalledWith('/projects/aurora%2Faiops');
  });

  it('submits the exact install body and returns the durable task', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'task-1', status: 'queued' }));
    const input = {
      serverId: 'server-1',
      version: '0.1.1',
      configuration: { bootstrapAdminUser: 'admin', bootstrapAdminPassword: 'long-password-123' },
    };
    await expect(installProject('aurora-aiops', input)).resolves.toEqual({ id: 'task-1', status: 'queued' });
    expect(postMock).toHaveBeenCalledWith('/projects/aurora-aiops/install', input);
  });
});
