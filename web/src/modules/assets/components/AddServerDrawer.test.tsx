import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { useState } from 'react';

import { renderWithProviders } from '../../../test/render';
import { AddServerDrawer } from './AddServerDrawer';
import { confirmAssetHostKey, createAssetServer } from '../api';
import type { AssetServer } from '../types';

vi.mock('../api', () => ({
  createAssetServer: vi.fn(),
  confirmAssetHostKey: vi.fn(),
}));

const createdServer = {
  id: 'server-1', name: 'edge-1', address: '192.0.2.10', username: 'root', sshPort: 22,
  status: 'pending' as const, cpuCores: 0, memoryBytes: 0, diskBytes: 0,
  createdAt: '2026-08-11T00:00:00Z', updatedAt: '2026-08-11T00:00:00Z',
  credentialAuthType: 'password' as const, credentialConfigured: true,
};

function ControlledDrawer({ onCreated = vi.fn() }: { onCreated?: (server: AssetServer) => void }) {
  const [open, setOpen] = useState(true);
  return <AddServerDrawer open={open} onClose={() => setOpen(false)} onCreated={onCreated} />;
}

async function fillPasswordForm(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('名称'), 'edge-1');
  await user.type(screen.getByLabelText('地址'), '192.0.2.10');
  await user.type(screen.getByLabelText('用户名'), 'root');
  await user.type(screen.getByLabelText('密码', { selector: 'input[type="password"]' }), 'test-password');
}

describe('AddServerDrawer', () => {
  beforeEach(() => vi.clearAllMocks());

  it('defaults to SSH port 22 and tests immediately, while showing only the selected credential fields', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ControlledDrawer />);

    expect(screen.getByLabelText('SSH 端口')).toHaveValue('22');
    expect(screen.getByLabelText('保存前测试连接')).toBeChecked();
    expect(screen.getByLabelText('密码', { selector: 'input[type="password"]' })).toBeInTheDocument();
    expect(screen.queryByLabelText('私钥', { selector: 'input[type="password"]' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('radio', { name: '私钥' }));
    expect(screen.queryByLabelText('密码', { selector: 'input[type="password"]' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('私钥', { selector: 'input[type="password"]' })).toBeInTheDocument();
    expect(screen.getByLabelText('私钥口令', { selector: 'input[type="password"]' })).toBeInTheDocument();
  });

  it('submits a pending server without connection testing and clears all credential controls after a successful reopen', async () => {
    const user = userEvent.setup();
    vi.mocked(createAssetServer).mockResolvedValue(createdServer);
    const onCreated = vi.fn();
    const { rerender } = renderWithProviders(<ControlledDrawer onCreated={onCreated} />);

    await fillPasswordForm(user);
    await user.click(screen.getByLabelText('保存前测试连接'));
    await user.click(screen.getByRole('button', { name: '保存服务器' }));

    await waitFor(() => expect(createAssetServer).toHaveBeenCalled());
    expect(vi.mocked(createAssetServer).mock.calls[0]?.[0]).toEqual({
      name: 'edge-1', address: '192.0.2.10', username: 'root', sshPort: 22,
      credentialAuthType: 'password', password: 'test-password', testConnection: false,
    });
    expect(onCreated).toHaveBeenCalledWith(createdServer);

    rerender(<AddServerDrawer open onClose={vi.fn()} onCreated={onCreated} />);
    await waitFor(() => expect(screen.getByLabelText('密码', { selector: 'input[type="password"]' })).toHaveValue(''));
    await user.click(screen.getByRole('radio', { name: '私钥' }));
    expect(screen.getByLabelText('私钥', { selector: 'input[type="password"]' })).toHaveValue('');
    expect(screen.getByLabelText('私钥口令', { selector: 'input[type="password"]' })).toHaveValue('');
  });

  it('shows a host-key fingerprint after 409 and keeps the already-created pending server when confirmation is cancelled', async () => {
    const user = userEvent.setup();
    const onCreated = vi.fn();
    vi.mocked(createAssetServer).mockRejectedValue({
      response: { status: 409, data: { data: { fingerprint: 'SHA256:untrusted', server: createdServer } } },
    });
    renderWithProviders(<ControlledDrawer onCreated={onCreated} />);

    await fillPasswordForm(user);
    await user.click(screen.getByRole('button', { name: '保存服务器' }));

    expect(await screen.findByText('SHA256:untrusted')).toBeInTheDocument();
    expect(onCreated).toHaveBeenCalledWith(createdServer);
    await user.click(screen.getByRole('button', { name: /取\s*消/ }));
    expect(confirmAssetHostKey).not.toHaveBeenCalled();
  });

  it('calls host-key confirmation only after the user explicitly confirms the displayed fingerprint', async () => {
    const user = userEvent.setup();
    vi.mocked(createAssetServer).mockRejectedValue({
      response: { status: 409, data: { data: { fingerprint: 'SHA256:untrusted', server: createdServer } } },
    });
    vi.mocked(confirmAssetHostKey).mockResolvedValue(undefined);
    renderWithProviders(<ControlledDrawer />);

    await fillPasswordForm(user);
    await user.click(screen.getByRole('button', { name: '保存服务器' }));
    await screen.findByText('SHA256:untrusted');
    expect(confirmAssetHostKey).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: '确认并信任' }));

    await waitFor(() => expect(confirmAssetHostKey).toHaveBeenCalledWith('server-1', 'SHA256:untrusted'));
  });
});
