import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App as AntApp } from 'antd';

import { http } from '../services/http';
import { loginWithPassword } from '../services/cluster';
import { useAppStore } from '../stores/appStore';
import { LoginPage } from './LoginPage';

vi.mock('../services/cluster', () => ({
  loginWithPassword: vi.fn(),
  logout: vi.fn(),
}));

function renderLoginPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <AntApp>
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <LoginPage />
        </MemoryRouter>
      </QueryClientProvider>
    </AntApp>,
  );
}

function buttonByText(text: string) {
  // Ant Design may normalize button text nodes, so compare the visible text without whitespace.
  const normalized = text.replace(/\s+/g, '');
  const button = screen
    .getAllByRole('button')
    .find((node) => node.textContent?.replace(/\s+/g, '') === normalized);
  if (!button) {
    throw new Error(`button with text ${JSON.stringify(text)} not found`);
  }
  return button;
}

describe('LoginPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  it('renders the neural access structure with labelled Signal Capsule fields', () => {
    renderLoginPage();
    expect(
      screen.getByRole('heading', { name: /AURORA\s*AIOPS\s*ACCESS\s*CONTROL/i }),
    ).toBeVisible();
    expect(screen.getByText('SYSTEM NODE: AURORA-CORE')).toBeVisible();
    expect(screen.getByLabelText('用户身份 / User Identity')).toBeVisible();
    expect(screen.getByLabelText('序列密钥 / Sequence Key')).toBeVisible();
    expect(screen.getByRole('button', { name: /INITIALIZE SESSION/i })).toBeVisible();
  });

  it('submits username and password to the platform login endpoint', async () => {
    vi.mocked(loginWithPassword).mockResolvedValue({
      id: 'u-admin',
      username: 'admin',
      role: 'admin',
      expiresAt: '2026-08-02T00:00:00Z',
    });
    renderLoginPage();

    fireEvent.change(screen.getByPlaceholderText('请输入平台用户名'), {
      target: { value: 'admin' },
    });
    fireEvent.change(screen.getByPlaceholderText('请输入密码'), {
      target: { value: 'correct-password' },
    });
    fireEvent.click(buttonByText('INITIALIZE SESSION ↗'));

    await waitFor(() => {
      expect(loginWithPassword).toHaveBeenCalledWith('admin', 'correct-password');
    });
  });

  it('does not read or write a Kubernetes token', () => {
    const { container } = renderLoginPage();
    expect(container.querySelector('input[name="token"]')).toBeNull();
    expect(container.querySelector('[name="ServiceAccount Token"]')).toBeNull();

    const persisted = JSON.parse(localStorage.getItem('aurora-aiops-app') ?? '{}');
    expect(persisted.state?.token).toBeUndefined();
  });

  it('configures axios with credentials so the session cookie is sent', () => {
    expect(http.defaults.baseURL).toBe('/api/v1');
    expect(http.defaults.withCredentials).toBe(true);
  });

  it('enters demo mode as local state without a session', async () => {
    renderLoginPage();
    fireEvent.click(screen.getByRole('button', { name: '进入演示模式' }));

    await waitFor(() => {
      expect(useAppStore.getState().authenticated).toBe(true);
      expect(useAppStore.getState().dataMode).toBe('demo');
    });
    expect(loginWithPassword).not.toHaveBeenCalled();
  });
});
