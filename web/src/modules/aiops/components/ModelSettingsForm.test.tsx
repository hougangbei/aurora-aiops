import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from '../../../test/render';
import { ModelSettingsForm } from './ModelSettingsForm';

describe('ModelSettingsForm', () => {
  it('never echoes an API key value, only a masked placeholder', () => {
    renderWithProviders(<ModelSettingsForm configuredKey />);
    const keyInput = screen.getByLabelText(/API Key（已配置）/i);
    expect(keyInput).toBeTruthy();
    expect(keyInput).toHaveProperty('type', 'password');
    expect((keyInput as HTMLInputElement).value).toBe('');
    expect((keyInput as HTMLInputElement).placeholder).toContain('••••••••');
    expect(screen.queryByText(/sk-[A-Za-z0-9]{8,}/)).toBeNull();
  });

  it('shows the keep-original hint when a key is already configured', () => {
    renderWithProviders(<ModelSettingsForm configuredKey />);
    expect(screen.getByText(/留空保存表示保留原值/)).toBeTruthy();
  });
});
