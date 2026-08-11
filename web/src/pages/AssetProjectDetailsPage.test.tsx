import userEvent from '@testing-library/user-event';
import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import { renderWithProviders } from '../test/render';
import { getProject } from '../modules/projects/api';
import { listAssetServers } from '../modules/assets/api';
import { AssetProjectDetailsPage } from './AssetProjectDetailsPage';
import { useAppStore } from '../stores/appStore';

vi.mock('../modules/projects/api', () => ({ getProject: vi.fn(), installProject: vi.fn() }));
vi.mock('../modules/assets/api', () => ({ listAssetServers: vi.fn() }));

describe('AssetProjectDetailsPage', () => {
  beforeEach(() => {
    vi.mocked(getProject).mockResolvedValue({ id: 'aurora-aiops', name: 'Aurora AIOps', description: '智能运维平台', versions: ['0.1.0', '0.1.1'], recommendedVersion: '0.1.1', supportedOsFamilies: ['linux'], supportedArchitectures: ['amd64', 'arm64'], installations: [{ serverId: 'server-1', serverName: 'edge-1', version: '0.1.0', status: 'succeeded', finishedAt: '2026-08-01T00:00:00Z' }] });
    vi.mocked(listAssetServers).mockResolvedValue([]);
    useAppStore.setState({ dataMode: 'live', user: { id: 'admin', username: 'admin', role: 'admin' } });
  });

  it('shows supported versions, targets, installation history and install action', async () => {
    const user = userEvent.setup();
    renderWithProviders(<MemoryRouter initialEntries={['/assets/projects/aurora-aiops']}><Routes><Route path="/assets/projects/:projectId" element={<AssetProjectDetailsPage />} /></Routes></MemoryRouter>);
    expect(await screen.findByText('Aurora AIOps')).toBeInTheDocument();
    expect(screen.getByText('支持版本')).toBeInTheDocument();
    expect(screen.getByText('0.1.1')).toBeInTheDocument();
    expect(screen.getByText(/linux/)).toBeInTheDocument();
    expect(screen.getByText(/amd64/)).toBeInTheDocument();
    expect(screen.getByText('edge-1')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '安装到服务器' }));
    expect(await screen.findByText('安装 Aurora AIOps')).toBeInTheDocument();
  });
});
