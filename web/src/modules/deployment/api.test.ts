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
  cancelDeploymentTask,
  getDeploymentTask,
  listDeploymentInstallations,
  listDeploymentTasks,
  retryDeploymentTask,
} from './api';

function envelope<T>(data: T) {
  return { data: { code: 'OK', message: 'success', data } };
}

describe('deployment api client', () => {
  beforeEach(() => {
    getMock.mockReset();
    postMock.mockReset();
  });

  it('uses exact server task and installation URLs', async () => {
    getMock.mockResolvedValue(envelope([]));
    await listDeploymentTasks('server/id');
    await listDeploymentInstallations('server/id');
    expect(getMock).toHaveBeenNthCalledWith(1, '/assets/servers/server%2Fid/tasks');
    expect(getMock).toHaveBeenNthCalledWith(2, '/assets/servers/server%2Fid/installations');
  });

  it('uses exact task detail, cancel and retry URLs', async () => {
    getMock.mockResolvedValue(envelope({ task: { id: 'task-1' }, steps: [] }));
    postMock.mockResolvedValue(envelope({ id: 'task-1' }));
    await getDeploymentTask('task/1');
    await cancelDeploymentTask('task/1');
    await retryDeploymentTask('task/1');
    expect(getMock).toHaveBeenCalledWith('/deployment-tasks/task%2F1');
    expect(postMock).toHaveBeenNthCalledWith(1, '/deployment-tasks/task%2F1/cancel');
    expect(postMock).toHaveBeenNthCalledWith(2, '/deployment-tasks/task%2F1/retry');
  });
});
