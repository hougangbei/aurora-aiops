import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it } from 'vitest';

import { renderWithProviders } from '../test/render';
import { useAppStore } from '../stores/appStore';
import { AppLayout } from './AppLayout';

describe('AppLayout responsive header', () => {
  beforeEach(() => {
    useAppStore.getState().clearSession();
    useAppStore.getState().enterDemo();
  });

  it('stacks controls and narrows the namespace selector below the sm breakpoint', () => {
    renderWithProviders(
      <MemoryRouter initialEntries={['/cluster/overview']}>
        <AppLayout>
          <div>page content</div>
        </AppLayout>
      </MemoryRouter>,
    );

    const headerInner = screen.getByRole('banner').firstElementChild;
    const namespaceSelect = screen.getByRole('combobox').closest('.ant-select');

    expect(headerInner).toHaveClass('flex-col', 'sm:flex-row');
    expect(namespaceSelect).toHaveStyle({ width: '132px' });
  });
});
