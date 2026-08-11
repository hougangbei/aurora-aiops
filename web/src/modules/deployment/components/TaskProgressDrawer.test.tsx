import { fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../../test/render';
import { cancelDeploymentTask, retryDeploymentTask } from '../api';
import { TaskProgressDrawer } from './TaskProgressDrawer';

vi.mock('../api', () => ({ cancelDeploymentTask: vi.fn(), retryDeploymentTask: vi.fn() }));
vi.mock('../useDeploymentEvents', () => ({
  useDeploymentEvents: vi.fn(),
}));

import { useDeploymentEvents } from '../useDeploymentEvents';

const task = {
  id: 'task-1', serverId: 'server-1', projectId: 'aurora', version: '1.0', actor: 'admin', action: 'install' as const,
  status: 'running' as const, percent: 40, cancelRequested: false, createdAt: '', updatedAt: '', currentStepLabel: '下载组件',
};

describe('TaskProgressDrawer', () => {
  beforeEach(() => {
    vi.mocked(cancelDeploymentTask).mockResolvedValue(task);
    vi.mocked(retryDeploymentTask).mockResolvedValue(task);
    vi.mocked(useDeploymentEvents).mockReturnValue({ task, steps: [
      { taskId: 'task-1', id: 'prepare', label: '准备主机', ordinal: 0, percent: 20, status: 'succeeded' },
      { taskId: 'task-1', id: 'download', label: '下载组件', ordinal: 1, percent: 40, status: 'running' },
    ], events: [{ id: 1, type: 'log', data: { message: 'password=should-not-appear' } }], connected: true, reconnecting: false, polling: false, failures: 0, lastEventId: 1, terminal: false });
  });

  it('renders durable steps, bounded redacted logs and admin cancel control', () => {
    renderWithProviders(<TaskProgressDrawer taskId="task-1" open canManage onClose={vi.fn()} />);
    expect(screen.getByText('准备主机')).toBeInTheDocument();
    expect(screen.getAllByText('下载组件').length).toBeGreaterThan(0);
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
    expect(screen.queryByText('password=should-not-appear')).not.toBeInTheDocument();
    expect(screen.getByText(/REDACTED/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '取消任务' })).toBeEnabled();
  });

  it('disables mutations for viewers and shows terminal state', () => {
    vi.mocked(useDeploymentEvents).mockReturnValue({ task: { ...task, status: 'failed', percent: 70, errorMessage: 'step failed' }, steps: [], events: [], connected: false, reconnecting: false, polling: false, failures: 0, lastEventId: 1, terminal: true });
    renderWithProviders(<TaskProgressDrawer taskId="task-1" open canManage={false} onClose={vi.fn()} />);
    expect(screen.getAllByText('任务失败').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: '重试任务' })).toBeDisabled();
  });

  it('calls cancel and retry endpoints from controls', async () => {
    renderWithProviders(<TaskProgressDrawer taskId="task-1" open canManage onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: '取消任务' }));
    await waitFor(() => expect(cancelDeploymentTask).toHaveBeenCalledWith('task-1'));
  });
});
