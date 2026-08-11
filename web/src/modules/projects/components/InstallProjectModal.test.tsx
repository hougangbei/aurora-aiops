import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../../test/render';
import { installProject } from '../api';
import { InstallProjectModal } from './InstallProjectModal';
import type { Project } from '../types';
import type { AssetServer } from '../../assets/types';

vi.mock('../api', () => ({ installProject: vi.fn() }));
vi.mock('../../deployment/components/TaskProgressDrawer', () => ({
  TaskProgressDrawer: ({ open }: { open: boolean }) => (open ? <div>任务进度抽屉</div> : null),
}));

const project: Project = {
  id: 'aurora-aiops', name: 'Aurora AIOps', description: '智能运维平台', versions: ['0.1.0', '0.1.1'],
  recommendedVersion: '0.1.1', supportedOsFamilies: ['linux'], supportedArchitectures: ['amd64'], installedServerCount: 1,
};
const servers: AssetServer[] = [
  { id: 'good', name: '在线主机', address: '192.0.2.10', username: 'root', sshPort: 22, status: 'online', architecture: 'amd64', osFamily: 'linux', hostKeyConfirmed: true, cpuCores: 2, memoryBytes: 1, diskBytes: 1, createdAt: '', updatedAt: '', credentialAuthType: 'password', credentialConfigured: true },
  { id: 'wrong-arch', name: '架构不兼容', address: '192.0.2.11', username: 'root', sshPort: 22, status: 'online', architecture: 'arm64', osFamily: 'linux', hostKeyConfirmed: true, cpuCores: 2, memoryBytes: 1, diskBytes: 1, createdAt: '', updatedAt: '', credentialAuthType: 'password', credentialConfigured: true },
  { id: 'offline', name: '离线主机', address: '192.0.2.12', username: 'root', sshPort: 22, status: 'offline', architecture: 'amd64', osFamily: 'linux', hostKeyConfirmed: true, cpuCores: 0, memoryBytes: 0, diskBytes: 0, createdAt: '', updatedAt: '', credentialAuthType: 'password', credentialConfigured: true },
  { id: 'untrusted', name: '待确认主机', address: '192.0.2.13', username: 'root', sshPort: 22, status: 'online', architecture: 'amd64', osFamily: 'linux', hostKeyConfirmed: false, cpuCores: 2, memoryBytes: 1, diskBytes: 1, createdAt: '', updatedAt: '', credentialAuthType: 'password', credentialConfigured: true },
];

describe('InstallProjectModal', () => {
  beforeEach(() => {
    vi.mocked(installProject).mockResolvedValue({ id: 'task-1', status: 'queued' } as never);
  });

  it('filters unsafe servers, defaults the recommended version, validates credentials and submits', async () => {
    const user = userEvent.setup();
    renderWithProviders(<InstallProjectModal open project={project} servers={servers} canInstall onClose={vi.fn()} />);

    expect(screen.getByText('0.1.1（推荐）')).toBeInTheDocument();
    expect(screen.getAllByText(/在线主机/).length).toBeGreaterThan(0);
    expect(screen.getByText(/架构不兼容/)).toBeInTheDocument();
    expect(screen.getByText(/离线主机/)).toBeInTheDocument();
    expect(screen.getByText(/待确认主机/)).toBeInTheDocument();

    await user.type(screen.getByLabelText('初始管理员用户名'), 'admin');
    await user.type(screen.getByLabelText('初始管理员密码'), 'long-password-123');
    await user.type(screen.getByLabelText('确认初始管理员密码'), 'long-password-123');
    await user.click(screen.getByLabelText('我确认在目标服务器安装 Aurora AIOps'));
    await user.click(screen.getByRole('button', { name: '开始安装' }));

    await waitFor(() => expect(installProject).toHaveBeenCalledWith('aurora-aiops', {
      serverId: 'good', version: '0.1.1', configuration: { bootstrapAdminUser: 'admin', bootstrapAdminPassword: 'long-password-123' },
    }));
    expect(await screen.findByText('任务进度抽屉')).toBeInTheDocument();
  });

  it.each(['viewer', 'operator'])('disables installation for %s', (role) => {
    renderWithProviders(<InstallProjectModal open project={project} servers={servers} canInstall={false} disabledReason={`当前角色 ${role} 无权安装`} onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: '开始安装' })).toBeDisabled();
    expect(screen.getByText(`当前角色 ${role} 无权安装`)).toBeInTheDocument();
  });

  it('clears credential fields after closing and reopening', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { rerender } = renderWithProviders(<InstallProjectModal open project={project} servers={servers} canInstall onClose={onClose} />);
    await user.type(screen.getByLabelText('初始管理员密码'), 'secret-value-123');
    await user.click(screen.getByRole('button', { name: '取消' }));
    rerender(<InstallProjectModal open={false} project={project} servers={servers} canInstall onClose={onClose} />);
    rerender(<InstallProjectModal open project={project} servers={servers} canInstall onClose={onClose} />);
    expect(screen.queryByDisplayValue('secret-value-123')).not.toBeInTheDocument();
  });
});
