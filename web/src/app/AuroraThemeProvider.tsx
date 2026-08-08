import { ConfigProvider, theme, type ThemeConfig } from 'antd';
import { type PropsWithChildren, useEffect } from 'react';

export const auroraTheme: ThemeConfig = {
  algorithm: theme.darkAlgorithm,
  token: {
    colorPrimary: '#718dff',
    colorInfo: '#718dff',
    colorSuccess: '#34d399',
    colorWarning: '#fbbf24',
    colorError: '#fb7185',
    colorBgBase: '#0a0f1c',
    colorBgLayout: '#0a0f1c',
    colorBgContainer: '#141c2f',
    colorBgElevated: '#1c2948',
    colorText: '#f0f4fa',
    colorTextSecondary: 'rgba(226, 235, 255, 0.68)',
    colorTextTertiary: 'rgba(226, 235, 255, 0.48)',
    colorBorder: 'rgba(178, 194, 255, 0.28)',
    colorBorderSecondary: 'rgba(178, 194, 255, 0.18)',
    borderRadius: 16,
    fontFamily: '"PingFang SC", "Microsoft YaHei", sans-serif',
    boxShadow: '0 18px 50px rgba(2, 6, 23, 0.36)',
    boxShadowSecondary: '0 12px 34px rgba(2, 6, 23, 0.28)',
  },
  components: {
    Button: {
      defaultBg: 'rgba(28, 41, 72, 0.72)',
      defaultBorderColor: 'rgba(178, 194, 255, 0.28)',
      defaultColor: '#e7edff',
      defaultHoverBg: 'rgba(63, 84, 145, 0.54)',
      defaultHoverBorderColor: 'rgba(184, 196, 255, 0.68)',
      defaultHoverColor: '#ffffff',
    },
    Card: {
      colorBgContainer: 'rgba(20, 28, 47, 0.88)',
    },
    Drawer: {
      colorBgElevated: '#101625',
    },
    Input: {
      activeBg: 'rgba(35, 49, 83, 0.86)',
      hoverBg: 'rgba(35, 49, 83, 0.74)',
      colorBgContainer: 'rgba(28, 41, 72, 0.72)',
    },
    Modal: {
      contentBg: '#141c2f',
      headerBg: '#141c2f',
    },
    Progress: {
      defaultColor: '#718dff',
      remainingColor: 'rgba(178, 194, 255, 0.14)',
    },
    Select: {
      colorBgContainer: 'rgba(28, 41, 72, 0.72)',
      optionSelectedBg: 'rgba(113, 141, 255, 0.2)',
    },
    Table: {
      headerBg: 'rgba(28, 41, 72, 0.82)',
      headerColor: 'rgba(231, 237, 255, 0.86)',
      rowHoverBg: 'rgba(113, 141, 255, 0.09)',
      borderColor: 'rgba(178, 194, 255, 0.16)',
    },
    Tooltip: {
      colorBgSpotlight: '#253459',
    },
  },
};

export function AuroraThemeProvider({ children }: PropsWithChildren) {
  useEffect(() => {
    document.body.classList.add('aurora-portals');

    return () => {
      document.body.classList.remove('aurora-portals');
    };
  }, []);

  return (
    <ConfigProvider theme={auroraTheme}>
      <div className="aurora-app dark" data-theme="aurora">
        {children}
      </div>
    </ConfigProvider>
  );
}
