import { Tag } from 'antd';
import type { IncidentStatus } from '../types';

const statusColor: Record<IncidentStatus, string> = {
  received: 'default',
  triaging: 'processing',
  collecting: 'processing',
  analyzing: 'processing',
  proposing: 'gold',
  awaiting_approval: 'gold',
  approved: 'blue',
  executing: 'blue',
  resolved: 'green',
  rejected: 'red',
  failed: 'red',
};

const statusLabel: Record<IncidentStatus, string> = {
  received: '已接收',
  triaging: '分类中',
  collecting: '采集中',
  analyzing: '分析中',
  proposing: '建议中',
  awaiting_approval: '待审批',
  approved: '已审批',
  executing: '执行中',
  resolved: '已解决',
  rejected: '已拒绝',
  failed: '失败',
};

export function IncidentStatusTag({ status }: { status: IncidentStatus }) {
  return <Tag color={statusColor[status]}>{statusLabel[status]}</Tag>;
}
