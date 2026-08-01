import type { Incident } from './types';

// 演示数据：仅用于 dataMode === 'demo' 时展示，不伪造实时趋势。
export const demoIncidents: Incident[] = [
  {
    id: 'inc-demo-1',
    summary: 'Pod crash loop',
    severity: 'critical',
    status: 'awaiting_approval',
    namespace: 'verify',
    resourceKind: 'Pod',
    resourceName: 'client',
    createdAt: '2026-08-01T08:00:00.000Z',
    updatedAt: '2026-08-01T08:30:00.000Z',
  },
  {
    id: 'inc-demo-2',
    summary: 'coredns 查询高延迟',
    severity: 'warning',
    status: 'collecting',
    namespace: 'kube-system',
    resourceKind: 'Deployment',
    resourceName: 'coredns',
    createdAt: '2026-08-01T07:00:00.000Z',
    updatedAt: '2026-08-01T07:20:00.000Z',
  },
  {
    id: 'inc-demo-3',
    summary: 'PVC 绑定超时',
    severity: 'info',
    status: 'resolved',
    namespace: 'default',
    resourceKind: 'PVC',
    resourceName: 'data-0',
    createdAt: '2026-07-31T18:00:00.000Z',
    updatedAt: '2026-08-01T06:00:00.000Z',
  },
  {
    id: 'inc-demo-4',
    summary: 'Node 磁盘使用率告警',
    severity: 'warning',
    status: 'failed',
    namespace: 'default',
    resourceKind: 'Service',
    resourceName: 'metrics',
    createdAt: '2026-07-31T09:00:00.000Z',
    updatedAt: '2026-07-31T09:15:00.000Z',
  },
];
