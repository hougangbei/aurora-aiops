import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useAppStore } from '../stores/appStore';
import { renderWithProviders } from '../test/render';
import { AIOpsSettingsPage } from './AIOpsSettingsPage';

const getAIOpsReadinessMock = vi.fn();

vi.mock('../modules/aiops/api', () => ({
  getAIOpsReadiness: (...args: unknown[]) => getAIOpsReadinessMock(...args),
}));

describe('AIOpsSettingsPage', () => {
  beforeEach(() => {
    useAppStore.setState({ authenticated: true, dataMode: 'live' });
    getAIOpsReadinessMock.mockReset();
  });

  it('shows environment configuration truthfully instead of a fake save form', async () => {
    getAIOpsReadinessMock.mockResolvedValue({
      modelConfigured: false,
      model: '',
      configurationSource: 'environment',
      runtimeMutable: false,
      deterministicRolesAvailable: true,
      remediationAvailable: true,
    });

    renderWithProviders(<AIOpsSettingsPage />);

    expect(await screen.findByText('模型未配置')).toBeVisible();
    expect(screen.getByText('KUBEJOJO_LLM_BASE_URL')).toBeVisible();
    expect(screen.getByText('KUBEJOJO_LLM_API_KEY')).toBeVisible();
    expect(screen.queryByRole('button', { name: '保 存' })).toBeNull();
    expect(screen.queryByText(/配置已保存/)).toBeNull();
  });
});
