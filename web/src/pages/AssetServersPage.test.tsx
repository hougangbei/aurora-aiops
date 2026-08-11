import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import { renderWithProviders } from '../test/render';
import { listAssetServers } from '../modules/assets/api';
import { AssetServersPage } from './AssetServersPage';
import { useAppStore } from '../stores/appStore';

vi.mock('../modules/assets/api', () => ({ listAssetServers: vi.fn(), createAssetServer: vi.fn(), confirmAssetHostKey: vi.fn() }));

const servers = [
  { id: 'online', name: 'edge-online', address: '192.0.2.10', username: 'root', sshPort: 22, status: 'online', cpuCores: 2, memoryBytes: 1, diskBytes: 1, createdAt: '', updatedAt: '', credentialAuthType: 'password', credentialConfigured: true },
  { id: 'pending', name: 'edge-pending', address: '192.0.2.11', username: 'root', sshPort: 22, status: 'pending', cpuCores: 0, memoryBytes: 0, diskBytes: 0, createdAt: '', updatedAt: '', credentialAuthType: 'password', credentialConfigured: true },
  { id: 'offline', name: 'edge-offline', address: '192.0.2.12', username: 'root', sshPort: 22, status: 'offline', statusMessage: 'SSH timeout', cpuCores: 0, memoryBytes: 0, diskBytes: 0, createdAt: '', updatedAt: '', credentialAuthType: 'private_key', credentialConfigured: true },
  { id: 'error', name: 'edge-error', address: '192.0.2.13', username: 'root', sshPort: 22, status: 'error', statusMessage: 'collection failed', cpuCores: 0, memoryBytes: 0, diskBytes: 0, createdAt: '', updatedAt: '', credentialAuthType: 'password', credentialConfigured: true },
] as const;

describe('AssetServersPage', () => {
  beforeEach(() => {
    vi.mocked(listAssetServers).mockResolvedValue([...servers]);
    useAppStore.setState({ dataMode: 'live', user: { id: 'admin', username: 'admin', role: 'admin' } });
  });

  it('shows inventory metrics, pending/offline state, searches, and navigates to server details', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <MemoryRouter initialEntries={['/assets/servers']}>
        <Routes>
          <Route path="/assets/servers" element={<AssetServersPage />} />
          <Route path="/assets/servers/:id" element={<div>server details route</div>} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText('edge-pending')).toBeInTheDocument();
    expect(screen.getByText('总数')).toBeInTheDocument();
    expect(screen.getByText('在线')).toBeInTheDocument();
    expect(screen.getByText('待连接')).toBeInTheDocument();
    expect(screen.getByText('离线/错误')).toBeInTheDocument();
    expect(screen.getByText('pending')).toBeInTheDocument();
    expect(screen.getByText('offline')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增服务器' })).toBeEnabled();

    await user.type(screen.getByPlaceholderText('搜索名称、地址或用户名'), 'offline');
    expect(screen.getByText('edge-offline')).toBeInTheDocument();
    expect(screen.queryByText('edge-pending')).not.toBeInTheDocument();

    await user.clear(screen.getByPlaceholderText('搜索名称、地址或用户名'));
    await user.click(screen.getByText('edge-pending'));
    expect(await screen.findByText('server details route')).toBeInTheDocument();
  });

  it.each(['viewer', 'operator'])("disables creation and clearly marks %s as read-only", async (role) => {
    useAppStore.setState({ dataMode: 'live', user: { id: role, username: role, role } });
    renderWithProviders(<MemoryRouter><AssetServersPage /></MemoryRouter>);

    expect(await screen.findByText('edge-pending')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新增服务器' })).toBeDisabled();
    expect(screen.getByText('当前角色为只读，无法新增服务器。')).toBeInTheDocument();
  });

  it('disables mutation controls in demo mode', async () => {
    useAppStore.setState({ dataMode: 'demo', user: { id: 'demo', username: 'demo', role: 'viewer' } });
    renderWithProviders(<MemoryRouter><AssetServersPage /></MemoryRouter>);

    expect(await screen.findByRole('button', { name: '新增服务器' })).toBeDisabled();
    expect(screen.getByText('演示模式为只读。')).toBeInTheDocument();
  });
});
