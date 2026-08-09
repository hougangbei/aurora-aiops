import { LogoutOutlined, MenuOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AxiosError } from 'axios';
import { Button, Drawer, Grid, Select, Space, Tag, Typography } from 'antd';
import { PropsWithChildren, startTransition, useEffect, useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';

import { PageErrorBoundary } from '../app/PageErrorBoundary';
import { BrandLogo } from '../components/brand/BrandLogo';
import { VersionBadge } from '../components/system/VersionBadge';
import { getAuthMe, getNamespaces, logout } from '../services/cluster';
import { useAppStore } from '../stores/appStore';
import { findNavigationItem, navigationSections } from './navigation';

type NavigationPanelProps = {
  currentPath: string;
  expandedSection: string | null;
  onNavigate: (path: string) => void;
  onToggleSection: (key: string, defaultPath: string, isActive: boolean) => void;
};

const demoNamespaces = ['default', 'kube-system', 'kube-public', 'kube-node-lease'];

function NavigationPanel({
  currentPath,
  expandedSection,
  onNavigate,
  onToggleSection,
}: NavigationPanelProps) {
  return (
    <div className="aurora-navigation flex h-full flex-col">
      <div className="aurora-navigation__brand border-b px-5 py-5">
        <div className="flex items-center gap-3">
          <BrandLogo size={42} />
          <div className="min-w-0 flex-1">
            <div className="flex flex-col">
              <span className="aurora-navigation__title text-lg font-bold">Aurora AIOps</span>
              <VersionBadge />
            </div>
          </div>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        <div className="space-y-1.5">
          {navigationSections.map((section) => {
            const sectionPath = section.items[0]?.path ?? '/';
            const sectionActive = section.items.some(
              (item) => currentPath === item.path || currentPath.startsWith(`${item.path}/`),
            );
            const expanded = expandedSection === section.key;

            return (
              <section key={section.key} className="rounded-xl">
                <button
                  type="button"
                  onClick={() => onToggleSection(section.key, sectionPath, sectionActive)}
                  className={[
                    'flex w-full items-center rounded-lg px-3 py-2 text-left transition-[background-color,color,box-shadow] duration-250 ease-out',
                    sectionActive
                      ? 'aurora-nav-section--active'
                      : 'aurora-nav-section--idle',
                  ].join(' ')}
                >
                  <div className="flex min-w-0 items-center gap-3">
                    <span
                      className={
                        sectionActive
                          ? 'aurora-nav-section__icon--active'
                          : 'aurora-nav-section__icon--idle'
                      }
                    >
                      {section.icon}
                    </span>
                    <div className="min-w-0">
                      <div
                        className={[
                          'text-[14px]',
                          sectionActive ? 'font-semibold' : 'font-medium',
                        ].join(' ')}
                      >
                        {section.label}
                      </div>
                    </div>
                  </div>
                </button>

                <div
                  className={[
                    'grid overflow-hidden transition-[grid-template-rows,opacity,margin] duration-300 ease-out',
                    expanded ? 'mt-1 grid-rows-[1fr] opacity-100' : 'mt-0 grid-rows-[0fr] opacity-0',
                  ].join(' ')}
                >
                  <div className="min-h-0 overflow-hidden">
                    <div className="space-y-0.5 pl-4">
                      {section.items.map((item) => {
                        const active =
                          currentPath === item.path || currentPath.startsWith(`${item.path}/`);

                        return (
                          <button
                            key={item.key}
                            type="button"
                            onClick={() => onNavigate(item.path)}
                            className={[
                              'flex w-full items-center justify-between rounded-lg px-3 py-1.5 text-left transition-[background-color,color] duration-200 ease-out',
                              active
                                ? 'aurora-nav-item--active'
                                : 'aurora-nav-item--idle',
                            ].join(' ')}
                          >
                            <div className="flex min-w-0 items-center gap-3">
                              <span
                                className={[
                                  'shrink-0 transition-all duration-200 ease-out',
                                  active
                                    ? 'aurora-nav-marker--active h-4 w-1 rounded-full'
                                    : 'aurora-nav-marker--idle h-1.5 w-1.5 rounded-full',
                                ].join(' ')}
                              />
                              <span
                                className={[
                                  'truncate text-[13px]',
                                  active ? 'font-semibold' : 'font-medium',
                                ].join(' ')}
                              >
                                {item.label}
                              </span>
                            </div>
                          </button>
                        );
                      })}
                    </div>
                  </div>
                </div>
              </section>
            );
          })}
        </div>
      </div>
    </div>
  );
}

export function AppLayout({ children }: PropsWithChildren) {
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const screens = Grid.useBreakpoint();
  const [drawerOpen, setDrawerOpen] = useState(false);

  const authenticated = useAppStore((state) => state.authenticated);
  const namespace = useAppStore((state) => state.namespace);
  const setNamespace = useAppStore((state) => state.setNamespace);
  const userName = useAppStore((state) => state.userName);
  const setUserName = useAppStore((state) => state.setUserName);
  const dataMode = useAppStore((state) => state.dataMode);
  const clearSession = useAppStore((state) => state.clearSession);

  const authQuery = useQuery({
    queryKey: ['auth-me'],
    queryFn: getAuthMe,
    enabled: dataMode === 'live' && authenticated,
  });

  const namespacesQuery = useQuery({
    queryKey: ['namespaces'],
    queryFn: getNamespaces,
    enabled: dataMode === 'live' && authenticated,
  });

  useEffect(() => {
    if (authQuery.data?.username) {
      setUserName(authQuery.data.username);
    }
  }, [authQuery.data?.username, setUserName]);

  useEffect(() => {
    const authStatus =
      authQuery.error instanceof AxiosError ? authQuery.error.response?.status : undefined;
    const namespacesStatus =
      namespacesQuery.error instanceof AxiosError
        ? namespacesQuery.error.response?.status
        : undefined;

    if (
      dataMode === 'live' &&
      [authStatus, namespacesStatus].some((status) => status === 401 || status === 403)
    ) {
      queryClient.clear();
      clearSession();
      navigate('/login', { replace: true });
    }
  }, [
    authQuery.error,
    clearSession,
    navigate,
    namespacesQuery.error,
    queryClient,
    dataMode,
  ]);

  const namespaces = dataMode === 'demo' ? demoNamespaces : namespacesQuery.data ?? [];
  const namespaceOptions = [
    { label: '全部命名空间', value: 'all' },
    ...namespaces
      .filter((item) => item !== 'all' && item !== 'all-namespaces')
      .map((item) => ({ label: item, value: item })),
  ];

  const activeItem = useMemo(() => findNavigationItem(location.pathname), [location.pathname]);
  const activeSectionKey =
    activeItem?.sectionKey &&
    navigationSections.some((section) => section.key === activeItem.sectionKey)
      ? activeItem.sectionKey
      : navigationSections[0]?.key ?? 'cluster';
  const [expandedSection, setExpandedSection] = useState<string | null>(activeSectionKey);

  useEffect(() => {
    setExpandedSection(activeSectionKey);
  }, [activeSectionKey]);

  const handleNavigate = (path: string) => {
    startTransition(() => navigate(path));
    setDrawerOpen(false);
  };

  const handleToggleSection = (key: string, defaultPath: string, isActive: boolean) => {
    if (!isActive) {
      setExpandedSection(key);
      startTransition(() => navigate(defaultPath));
      setDrawerOpen(false);
      return;
    }

    setExpandedSection((current) => (current === key ? null : key));
  };

  const handleLogout = () => {
    queryClient.clear();
    void logout().finally(() => {
      clearSession();
      navigate('/login', { replace: true });
    });
  };

  return (
    <div className="aurora-shell min-h-screen">
      {screens.lg ? (
        <aside className="aurora-sidebar fixed inset-y-0 left-0 z-30 w-[256px] border-r">
          <NavigationPanel
            currentPath={location.pathname}
            expandedSection={expandedSection}
            onNavigate={handleNavigate}
            onToggleSection={handleToggleSection}
          />
        </aside>
      ) : (
        <Drawer
          placement="left"
          open={drawerOpen}
          onClose={() => setDrawerOpen(false)}
          width={256}
          closable={false}
          styles={{ body: { padding: 0 } }}
        >
          <NavigationPanel
            currentPath={location.pathname}
            expandedSection={expandedSection}
            onNavigate={handleNavigate}
            onToggleSection={handleToggleSection}
          />
        </Drawer>
      )}

      <div className="min-h-screen lg:pl-[256px]">
        <header className="aurora-header sticky top-0 z-20 border-b backdrop-blur-xl">
          <div className="mx-auto flex max-w-[1440px] flex-col items-stretch justify-between gap-3 px-4 py-3 sm:flex-row sm:items-center sm:gap-4 sm:px-6">
            <div className="flex min-w-0 items-center gap-3 sm:w-auto">
              {!screens.lg ? (
                <Button icon={<MenuOutlined />} onClick={() => setDrawerOpen(true)} />
              ) : null}
              <div className="min-w-0">
                <div className="aurora-eyebrow text-xs font-semibold uppercase tracking-[0.18em]">
                  {activeItem?.sectionLabel ?? 'Aurora AIOps'}
                </div>
                <div className="mt-1 flex min-w-0 items-center gap-2">
                  <Typography.Title level={4} className="!mb-0 truncate">
                    {activeItem?.label ?? 'Overview'}
                  </Typography.Title>
                </div>
              </div>
            </div>

            <Space size={10} wrap className="w-full justify-between sm:w-auto sm:justify-end">
              <Space size={8} className="min-w-0">
                <Typography.Text type="secondary" className="hidden sm:inline">
                  Namespace
                </Typography.Text>
                <Select
                  value={namespace}
                  style={{ width: screens.sm ? 180 : 132 }}
                  options={namespaceOptions}
                  onChange={setNamespace}
                />
              </Space>
              <Tag color={dataMode === 'demo' ? 'gold' : 'geekblue'} className="rounded-full px-3 py-1">
                {userName}
              </Tag>
              <Button icon={<LogoutOutlined />} onClick={handleLogout}>
                退出
              </Button>
            </Space>
          </div>
        </header>

        <main className="aurora-main mx-auto max-w-[1440px] px-4 py-5 sm:px-6">
          <PageErrorBoundary resetKey={`${location.pathname}:${namespace}`}>
            {children}
          </PageErrorBoundary>
        </main>
      </div>
    </div>
  );
}
