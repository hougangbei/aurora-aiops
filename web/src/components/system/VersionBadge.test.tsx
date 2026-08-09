import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { getBuildInfo, getUpdateStatus } from '../../services/system';
import { useAppStore } from '../../stores/appStore';
import { renderWithProviders } from '../../test/render';
import { VersionBadge } from './VersionBadge';

vi.mock('../../services/system', () => ({
  getBuildInfo: vi.fn(),
  getUpdateStatus: vi.fn(),
}));

describe('VersionBadge warning tone', () => {
  beforeEach(() => {
    vi.mocked(getBuildInfo).mockResolvedValue({
      version: '0.1.1',
      commit: 'test',
      date: 'test',
      buildType: 'release',
      embeddedFrontend: true,
    });
    vi.mocked(getUpdateStatus).mockResolvedValue({
      currentVersion: '0.1.1',
      runningVersion: '0.1.1',
      installedVersion: '0.1.1',
      latestVersion: '0.1.1',
      hasUpdate: false,
      pendingRestart: false,
      primaryState: 'up_to_date',
      cached: false,
      warning: 'GitHub API rate limit exceeded',
      buildType: 'release',
      repository: 'hougangbei/aurora-aiops',
      updateEnabled: true,
      authorized: true,
      canInstall: false,
      canRollback: false,
      canRestart: false,
      message: 'Version check completed with warnings.',
      currentActor: 'admin',
      embeddedFrontend: true,
      backupAvailable: false,
    });
    useAppStore.getState().setDataMode('live');
  });

  it('shows recoverable update warnings as amber instead of red errors', async () => {
    renderWithProviders(
      <MemoryRouter>
        <VersionBadge />
      </MemoryRouter>,
    );

    expect(await screen.findByText('告警')).toHaveClass('bg-amber-100', 'text-amber-700');
  });
});
