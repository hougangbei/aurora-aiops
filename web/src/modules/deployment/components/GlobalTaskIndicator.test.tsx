import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MemoryRouter } from 'react-router-dom';

import { renderWithProviders } from '../../../test/render';
import type { DeploymentTask } from '../types';
import { GlobalTaskIndicator } from './GlobalTaskIndicator';

const task: DeploymentTask = { id: 'task-7', serverId: 'server-1', projectId: 'aurora', version: '1', actor: 'admin', action: 'install', status: 'running', percent: 33, cancelRequested: false, createdAt: '', updatedAt: '' };

describe('GlobalTaskIndicator', () => {
  it('links to the active task and is hidden when there are no active tasks', () => {
    renderWithProviders(<MemoryRouter><GlobalTaskIndicator tasks={[task]} /></MemoryRouter>);
    expect(screen.getByRole('link', { name: /任务进度/ })).toHaveAttribute('href', '/assets/servers/server-1?task=task-7');
  });

  it('does not render when no task is active', () => {
    renderWithProviders(<MemoryRouter><GlobalTaskIndicator tasks={[]} /></MemoryRouter>);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });
});
