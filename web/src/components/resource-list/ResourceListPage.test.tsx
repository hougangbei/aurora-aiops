import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from '../../test/render';
import { ResourceListPage } from './ResourceListPage';

describe('ResourceListPage loading state', () => {
  it('uses CoreSpinLoader while keeping the page header and refresh action', () => {
    renderWithProviders(
      <ResourceListPage
        title="Pods"
        description="当前集群中的 Pod"
        dataSource={[]}
        columns={[]}
        rowKey="name"
        loading
        onRefresh={() => undefined}
      />,
    );

    expect(screen.getByRole('status')).toBeVisible();
    expect(screen.getByText('Pods')).toBeVisible();
    expect(screen.getByRole('button', { name: /刷新/ })).toBeVisible();
  });
});
