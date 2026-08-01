import { screen } from '@testing-library/react';
import { Button } from 'antd';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from './render';

describe('component test harness', () => {
  it('renders a button and asserts visibility', () => {
    renderWithProviders(<Button>hello harness</Button>);
    expect(screen.getByRole('button', { name: /hello harness/i })).toBeVisible();
  });
});
