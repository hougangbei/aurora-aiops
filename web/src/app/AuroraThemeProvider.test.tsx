import { render, screen } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { AuroraThemeProvider, auroraTheme } from './AuroraThemeProvider';

describe('AuroraThemeProvider', () => {
  it('scopes the authenticated dark theme without changing the login root', () => {
    render(
      <AuroraThemeProvider>
        <div data-testid="authenticated-content">content</div>
      </AuroraThemeProvider>,
    );

    const themeRoot = screen.getByTestId('authenticated-content').closest('.aurora-app');

    expect(themeRoot).toHaveClass('dark');
    expect(themeRoot).toHaveAttribute('data-theme', 'aurora');
    expect(document.querySelector('.login-page')).toBeNull();
  });

  it('exposes the Aurora control-deck token contract', () => {
    expect(auroraTheme.token).toMatchObject({
      colorPrimary: '#718dff',
      colorBgLayout: '#0a0f1c',
      colorBgContainer: '#141c2f',
      colorText: '#f0f4fa',
    });
  });

  it('scopes portal surfaces to Aurora only while authenticated content is mounted', () => {
    const { unmount } = render(
      <AuroraThemeProvider>
        <div>content</div>
      </AuroraThemeProvider>,
    );

    expect(document.body).toHaveClass('aurora-portals');

    unmount();

    expect(document.body).not.toHaveClass('aurora-portals');
  });

  it('provides Aurora context to both App.useApp and static overlay APIs', () => {
    const configSpy = vi.spyOn(ConfigProvider, 'config');
    const { unmount } = render(
      <AuroraThemeProvider>
        <div data-testid="overlay-context-content">content</div>
      </AuroraThemeProvider>,
    );

    expect(screen.getByTestId('overlay-context-content').closest('.ant-app')).not.toBeNull();
    expect(configSpy).toHaveBeenCalledWith({ holderRender: expect.any(Function) });

    unmount();

    expect(configSpy).toHaveBeenLastCalledWith({ holderRender: undefined });
    configSpy.mockRestore();
  });
});
