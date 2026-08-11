import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import { renderWithProviders } from '../test/render';
import { collectAssetServer, getAssetServer, getLatestAssetSnapshot, listAssetSoftware, testAssetConnection } from '../modules/assets/api';
import { AssetServerDetailsPage } from './AssetServerDetailsPage';
import { useAppStore } from '../stores/appStore';
import { listDeploymentInstallations, listDeploymentTasks } from '../modules/deployment/api';

vi.mock('../modules/assets/api', () => ({
  getAssetServer: vi.fn(), getLatestAssetSnapshot: vi.fn(), listAssetSoftware: vi.fn(),
  collectAssetServer: vi.fn(), testAssetConnection: vi.fn(), confirmAssetHostKey: vi.fn(),
}));
vi.mock('../modules/deployment/api', () => ({ listDeploymentInstallations: vi.fn(), listDeploymentTasks: vi.fn() }));
vi.mock('../modules/deployment/components/TaskProgressDrawer', () => ({ TaskProgressDrawer: () => null }));

const server = {
  id: 'server-1', name: 'edge-offline', address: '192.0.2.12', username: 'root', sshPort: 22,
  status: 'offline' as const, statusMessage: 'SSH timeout', cpuCores: 0, memoryBytes: 0, diskBytes: 0,
  lastCollectedAt: '2026-08-01T00:00:00Z', createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-01T00:00:00Z',
  credentialAuthType: 'private_key' as const, credentialConfigured: true,
};

function renderDetails() {
  return renderWithProviders(
    <MemoryRouter initialEntries={['/assets/servers/server-1']}>
      <Routes><Route path="/assets/servers/:id" element={<AssetServerDetailsPage />} /></Routes>
    </MemoryRouter>,
  );
}

describe('AssetServerDetailsPage', () => {
  beforeEach(() => {
    vi.mocked(getAssetServer).mockResolvedValue(server);
    vi.mocked(getLatestAssetSnapshot).mockResolvedValue({
      id: 'snapshot-1', serverId: 'server-1', hostname: 'edge-offline', osFamily: 'linux', osVersion: '1', kernelVersion: '6.0', architecture: 'amd64', cpuCores: 2, memoryBytes: 1, diskBytes: 1, load1: 0.2, uptimeSeconds: 60, collectedAt: '2026-08-01T00:00:00Z',
    });
    vi.mocked(listAssetSoftware).mockResolvedValue([{ category: 'system', name: 'openssl', version: '3.0', architecture: 'amd64', source: 'apt', status: 'installed' }]);
    vi.mocked(listDeploymentTasks).mockResolvedValue([]);
    vi.mocked(listDeploymentInstallations).mockResolvedValue([]);
    useAppStore.setState({ dataMode: 'live', user: { id: 'admin', username: 'admin', role: 'admin' } });
  });

  it('shows a stale snapshot, server error state, software, and explicit next-phase tabs without calling unimplemented APIs', async () => {
    const user = userEvent.setup();
    renderDetails();

    expect(await screen.findByText('edge-offline')).toBeInTheDocument();
    expect(screen.getByText('SSH timeout')).toBeInTheDocument();
    expect(screen.getByText('最近采集时间（可能已过期）')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '概览' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '软件' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '安装记录' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '任务进度' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '测试连接' })).toBeEnabled();
    expect(screen.getByRole('button', { name: '采集资产' })).toBeEnabled();

    await user.click(screen.getByRole('tab', { name: '软件' }));
    expect(await screen.findByText('openssl')).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: '安装记录' }));
    expect(screen.getByRole('tab', { name: '安装记录' })).toHaveAttribute('aria-selected', 'true');
    await user.click(screen.getByRole('tab', { name: '任务进度' }));
    expect(await screen.findByText('暂无部署任务')).toBeInTheDocument();
    expect(getAssetServer).toHaveBeenCalledWith('server-1');
    expect(getLatestAssetSnapshot).toHaveBeenCalledWith('server-1');
    expect(listAssetSoftware).toHaveBeenCalledWith('server-1');
    expect(listDeploymentTasks).toHaveBeenCalledWith('server-1');
    expect(listDeploymentInstallations).toHaveBeenCalledWith('server-1');
    expect(collectAssetServer).not.toHaveBeenCalled();
    expect(testAssetConnection).not.toHaveBeenCalled();
  });

  it.each([
    ['viewer', true],
    ['operator', false],
  ])("applies safe role restrictions for %s without revealing credentials", async (role, disabled) => {
    useAppStore.setState({ dataMode: 'live', user: { id: role, username: role, role } });
    renderDetails();

    expect(await screen.findByText('edge-offline')).toBeInTheDocument();
    if (disabled) {
      expect(screen.getByRole('button', { name: '测试连接' })).toBeDisabled();
      expect(screen.getByRole('button', { name: '采集资产' })).toBeDisabled();
    } else {
      expect(screen.getByRole('button', { name: '测试连接' })).toBeEnabled();
      expect(screen.getByRole('button', { name: '采集资产' })).toBeEnabled();
    }
    expect(screen.queryByDisplayValue(/password|private key/i)).not.toBeInTheDocument();
  });
});
