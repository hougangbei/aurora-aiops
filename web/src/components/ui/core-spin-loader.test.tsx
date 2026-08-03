import { act, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../test/render';
import { CoreSpinLoader } from './core-spin-loader';

afterEach(() => {
  vi.useRealTimers();
});

describe('CoreSpinLoader', () => {
  it('renders a live status with the initial loading text and requested min height', () => {
    vi.useFakeTimers();
    renderWithProviders(<CoreSpinLoader minHeight="180px" />);

    const status = screen.getByRole('status');
    expect(status).toHaveAttribute('aria-live', 'polite');
    expect(status).toHaveStyle({ minHeight: '180px' });
    expect(screen.getByText('Initializing')).toBeVisible();
    expect(status.querySelectorAll('[aria-hidden="true"]')).not.toHaveLength(0);
  });

  it('cycles through the provided loading states', () => {
    vi.useFakeTimers();
    renderWithProviders(<CoreSpinLoader />);

    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByText('Loading...')).toBeVisible();
    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByText('Fetching Data..')).toBeVisible();
  });
});
