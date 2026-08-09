import { screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useAppStore } from '../stores/appStore';
import { renderWithProviders } from '../test/render';
import type { AgentRun, Incident } from '../modules/aiops/types';
import { IncidentDetailsPage } from './IncidentDetailsPage';

const getIncidentMock = vi.fn();
const getRunsMock = vi.fn();
const getEvidenceMock = vi.fn();
const getAIOpsReadinessMock = vi.fn();

vi.mock('../modules/aiops/api', () => ({
  getIncident: (...args: unknown[]) => getIncidentMock(...args),
  getRuns: (...args: unknown[]) => getRunsMock(...args),
  getEvidence: (...args: unknown[]) => getEvidenceMock(...args),
  getAIOpsReadiness: (...args: unknown[]) => getAIOpsReadinessMock(...args),
  reanalyzeIncident: vi.fn(),
}));

vi.mock('../modules/aiops/useIncidentEvents', () => ({
  useIncidentEvents: () => undefined,
}));

const incident: Incident = {
  id: 'inc-1',
  summary: 'CrashLoopBackOff：api-0',
  severity: 'warning',
  status: 'collecting',
  namespace: 'kubejojo-lab',
  resourceKind: 'Pod',
  resourceName: 'api-0',
  createdAt: '2026-08-09T04:00:00.000Z',
  updatedAt: '2026-08-09T04:01:00.000Z',
};

const skippedRootCause: AgentRun = {
  id: 'run-1',
  incidentId: 'inc-1',
  role: 'root_cause',
  attempt: 1,
  status: 'skipped',
  summary: 'model_unavailable',
  output: '',
  model: '',
  promptTokens: 0,
  completionTokens: 0,
  totalTokens: 0,
  error: '',
  startedAt: '2026-08-09T04:00:00.000Z',
  completedAt: '2026-08-09T04:00:01.000Z',
};

describe('IncidentDetailsPage blocked workflow guidance', () => {
  beforeEach(() => {
    useAppStore.setState({ authenticated: true, dataMode: 'live' });
    getIncidentMock.mockResolvedValue(incident);
    getRunsMock.mockResolvedValue({ runs: [skippedRootCause] });
    getEvidenceMock.mockResolvedValue({ nodes: [], edges: [] });
    getAIOpsReadinessMock.mockResolvedValue({ modelConfigured: false });
  });

  it('explains model blocking and links to the real configuration instructions', async () => {
    renderWithProviders(
      <MemoryRouter initialEntries={['/aiops/incidents/inc-1']}>
        <Routes>
          <Route path="/aiops/incidents/:id" element={<IncidentDetailsPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText('等待模型配置')).toBeVisible();
    expect(screen.getByText(/分类和证据采集已完成/)).toBeVisible();
    expect(screen.getByRole('link', { name: '查看模型配置说明' })).toHaveAttribute('href', '/aiops/settings');
  });
});
