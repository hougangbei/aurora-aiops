import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useAppStore } from '../stores/appStore';
import { renderWithProviders } from '../test/render';
import type { Incident } from '../modules/aiops/types';
import { AIOpsOverviewPage } from './AIOpsOverviewPage';

const listIncidentsMock = vi.fn();
const getAIOpsReadinessMock = vi.fn();
const getClusterConnectionMock = vi.fn();

vi.mock('../modules/aiops/api', () => ({
  listIncidents: (...args: unknown[]) => listIncidentsMock(...args),
  getAIOpsReadiness: (...args: unknown[]) => getAIOpsReadinessMock(...args),
  getClusterConnection: (...args: unknown[]) => getClusterConnectionMock(...args),
}));

const blockedIncident: Incident = {
  id: 'inc-blocked',
  summary: 'CrashLoopBackOff：api-0',
  severity: 'warning',
  status: 'collecting',
  namespace: 'aurora-aiops-lab',
  resourceKind: 'Pod',
  resourceName: 'api-0',
  createdAt: '2026-08-09T04:00:00.000Z',
  updatedAt: '2026-08-09T04:01:00.000Z',
};

describe('AIOpsOverviewPage event command center', () => {
  beforeEach(() => {
    useAppStore.setState({ authenticated: true, dataMode: 'live' });
    listIncidentsMock.mockReset();
    getAIOpsReadinessMock.mockReset();
    getClusterConnectionMock.mockReset();
  });

  it('surfaces runtime readiness and model-blocked incidents with next actions', async () => {
    listIncidentsMock.mockResolvedValue([blockedIncident]);
    getAIOpsReadinessMock.mockResolvedValue({
      modelConfigured: false,
      model: '',
      configurationSource: 'environment',
      runtimeMutable: false,
      deterministicRolesAvailable: true,
      remediationAvailable: true,
    });
    getClusterConnectionMock.mockResolvedValue({
      state: 'degraded',
      version: '1.30',
      latencyMs: 18,
      capabilities: { nodes: true, events: true, podLogs: true, metrics: false },
    });

    renderWithProviders(
      <MemoryRouter>
        <AIOpsOverviewPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: '事件指挥台' })).toBeVisible();
    expect(await screen.findByText('模型未配置')).toBeVisible();
    expect(await screen.findByText('Metrics 不可用')).toBeVisible();
    expect(await screen.findByText('CrashLoopBackOff：api-0')).toBeVisible();
    expect(screen.getByRole('link', { name: /^配置模型/ })).toHaveAttribute('href', '/aiops/settings');
  });
});
