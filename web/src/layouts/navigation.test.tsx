import { describe, expect, it } from 'vitest';

import { navigationItems, navigationSections } from './navigation';

describe('aiops navigation', () => {
  it('registers the 资产管理 section with an implemented servers route', () => {
    const section = navigationSections.find((s) => s.label === '资产管理');
    expect(section?.items).toEqual([
      expect.objectContaining({ label: '服务器', path: '/assets/servers', implemented: true }),
    ]);
    expect(navigationItems.find((item) => item.path === '/assets/servers')?.sectionLabel).toBe('资产管理');
  });

  it('registers the 智能运维 section', () => {
    const section = navigationSections.find((s) => s.label === '智能运维');
    expect(section).toBeDefined();
    expect(section?.items.length).toBe(6);
  });

  it('exposes seven unique aiops paths (six nav items plus the detail route)', () => {
    const aiopsNavPaths = navigationItems
      .filter((item) => item.sectionLabel === '智能运维')
      .map((item) => item.path);
    expect(aiopsNavPaths).toHaveLength(6);

    // 导航项自身路径唯一。
    expect(new Set(aiopsNavPaths).size).toBe(aiopsNavPaths.length);

    // 详情路由 /aiops/incidents/:id 与导航路径共同构成 7 个唯一路径。
    const allAIOpsPaths = [...aiopsNavPaths, '/aiops/incidents/:id'];
    expect(new Set(allAIOpsPaths).size).toBe(7);
  });

  it('keeps the incident list and detail on the same navigation item', () => {
    const incidentItem = navigationItems.find((item) => item.path === '/aiops/incidents');
    expect(incidentItem).toBeDefined();
    // 详情路径应按前缀匹配到 Incidents 导航项。
    const match = navigationItems
      .filter((item) => item.sectionLabel === '智能运维')
      .find((item) => '/aiops/incidents/inc-1'.startsWith(`${item.path}/`));
    expect(match?.path).toBe('/aiops/incidents');
  });
});
