import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../../test/render';
import { useAppStore } from '../../../stores/appStore';
import type { Incident } from '../types';
import { IncidentTable } from './IncidentTable';

const listIncidentsMock = vi.fn();
vi.mock('../api', () => ({
  listIncidents: (...args: unknown[]) => listIncidentsMock(...args),
}));

function LocationDisplay() {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}</div>;
}

function renderTable(initialEntries: string[] = ['/aiops/incidents']) {
  return renderWithProviders(
    <MemoryRouter initialEntries={initialEntries}>
      <LocationDisplay />
      <IncidentTable />
    </MemoryRouter>,
  );
}

const incident: Incident = {
  id: 'inc-1',
  summary: 'Pod crash loop',
  severity: 'critical',
  status: 'awaiting_approval',
  namespace: 'verify',
  resourceKind: 'Pod',
  resourceName: 'client',
  createdAt: '2026-08-01T08:00:00.000Z',
  updatedAt: '2026-08-01T08:30:00.000Z',
};

describe('IncidentTable', () => {
  beforeEach(() => {
    useAppStore.setState({ authenticated: true, dataMode: 'live' });
    listIncidentsMock.mockReset();
  });

  it('shows loading while fetching', () => {
    listIncidentsMock.mockReturnValue(new Promise(() => {}));
    const { container } = renderTable();
    expect(container.querySelector('.ant-spin-spinning')).toBeTruthy();
  });

  it('shows the empty message when there are no incidents', async () => {
    listIncidentsMock.mockResolvedValue([]);
    renderTable();
    await screen.findByText('暂无诊断任务');
  });

  it('shows an error alert when the query fails', async () => {
    listIncidentsMock.mockRejectedValue(new Error('boom'));
    renderTable();
    await screen.findByText('加载诊断任务失败');
  });

  it('renders severity with its color', async () => {
    listIncidentsMock.mockResolvedValue([incident]);
    renderTable();
    const tag = await screen.findByText('critical');
    expect(tag.closest('.ant-tag-red')).toBeTruthy();
  });

  it('passes the namespace filter from the URL query to the api', async () => {
    listIncidentsMock.mockResolvedValue([incident]);
    renderTable(['/aiops/incidents?namespace=verify']);
    await screen.findByText('Pod crash loop');
    await waitFor(() => {
      expect(listIncidentsMock).toHaveBeenCalledWith({ status: undefined, namespace: 'verify' });
    });
  });

  it('navigates to incident detail when the summary is clicked', async () => {
    listIncidentsMock.mockResolvedValue([incident]);
    renderTable();
    await screen.findByText('Pod crash loop');
    const user = userEvent.setup();
    await user.click(screen.getByRole('link', { name: /Pod crash loop/i }));
    await waitFor(() => {
      expect(screen.getByTestId('location').textContent).toBe('/aiops/incidents/inc-1');
    });
  });
});
