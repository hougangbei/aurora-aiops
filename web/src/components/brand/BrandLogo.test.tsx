import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from '../../test/render';
import { BrandLogo } from './BrandLogo';

describe('BrandLogo', () => {
  it('renders the AIOps Aurora mark with an accessible label and requested size', () => {
    renderWithProviders(<BrandLogo size={42} />);

    expect(screen.getByRole('img', { name: 'AIOps 平台 logo' })).toHaveStyle({
      width: '42px',
      height: '42px',
    });
    expect(screen.getByText('A')).toBeVisible();
  });
});
