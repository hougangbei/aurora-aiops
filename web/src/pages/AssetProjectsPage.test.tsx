import userEvent from '@testing-library/user-event';
import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import { renderWithProviders } from '../test/render';
import { listProjects } from '../modules/projects/api';
import { AssetProjectsPage } from './AssetProjectsPage';
import { useAppStore } from '../stores/appStore';

vi.mock('../modules/projects/api', () => ({ listProjects: vi.fn() }));

const projects = [{ id: 'aurora-aiops', name: 'Aurora AIOps', description: '智能运维平台', versions: ['0.1.0', '0.1.1'], recommendedVersion: '0.1.1', supportedOsFamilies: ['linux'], supportedArchitectures: ['amd64', 'arm64'], installedServerCount: 2 }];

describe('AssetProjectsPage', () => {
  beforeEach(() => {
    vi.mocked(listProjects).mockResolvedValue(projects);
    useAppStore.setState({ dataMode: 'live', user: { id: 'admin', username: 'admin', role: 'admin' } });
  });

  it('shows project cards with release metadata and navigates to details', async () => {
    const user = userEvent.setup();
    renderWithProviders(<MemoryRouter initialEntries={['/assets/projects']}><Routes><Route path="/assets/projects" element={<AssetProjectsPage />} /><Route path="/assets/projects/:projectId" element={<div>项目详情路由</div>} /></Routes></MemoryRouter>);
    expect(await screen.findByText('Aurora AIOps')).toBeInTheDocument();
    expect(screen.getByText('智能运维平台')).toBeInTheDocument();
    expect(screen.getByText('推荐版本：0.1.1')).toBeInTheDocument();
    expect(screen.getByText(/amd64/)).toBeInTheDocument();
    expect(screen.getByText('已安装 2 台服务器')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '查看详情' }));
    expect(await screen.findByText('项目详情路由')).toBeInTheDocument();
  });
});
