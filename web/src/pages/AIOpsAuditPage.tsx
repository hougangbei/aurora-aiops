import { Alert, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { formatTime } from '../modules/aiops/format';

type AuditEntry = {
  id: string;
  actor: string;
  action: string;
  target: string;
  result: 'success' | 'rejected' | 'failed';
  timestamp: string;
  hashValid: boolean;
};

// 演示审计数据：审计接口待接入（计划 04），当前展示列结构与校验语义。
const demoAudit: AuditEntry[] = [
  {
    id: 'aud-1',
    actor: 'admin',
    action: 'approve-remediation',
    target: 'inc-demo-1',
    result: 'success',
    timestamp: '2026-08-01T08:35:00.000Z',
    hashValid: true,
  },
  {
    id: 'aud-2',
    actor: 'operator',
    action: 'reanalyze',
    target: 'inc-demo-2',
    result: 'rejected',
    timestamp: '2026-08-01T07:25:00.000Z',
    hashValid: false,
  },
];

const columns: ColumnsType<AuditEntry> = [
  { title: 'Actor', dataIndex: 'actor', key: 'actor' },
  { title: 'Action', dataIndex: 'action', key: 'action' },
  { title: 'Target', dataIndex: 'target', key: 'target' },
  {
    title: 'Result',
    dataIndex: 'result',
    key: 'result',
    render: (result: AuditEntry['result']) => (
      <Tag color={result === 'success' ? 'green' : result === 'rejected' ? 'orange' : 'red'}>{result}</Tag>
    ),
  },
  { title: '时间戳', dataIndex: 'timestamp', key: 'timestamp', render: formatTime },
  {
    title: 'Hash 校验',
    dataIndex: 'hashValid',
    key: 'hashValid',
    render: (valid: boolean) => (valid ? <Tag color="green">通过</Tag> : <Tag color="red">失败</Tag>),
  },
];

export function AIOpsAuditPage() {
  return (
    <section className="space-y-4 rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <div>
        <Typography.Title level={2} className="!mb-1">
          操作审计
        </Typography.Title>
        <Typography.Text type="secondary">actor、action、target、result、时间戳与证据 Hash 校验状态。</Typography.Text>
      </div>

      <Alert type="info" showIcon message="审计接口将在后续计划接入，当前展示演示数据。" />

      <Table<AuditEntry> rowKey="id" columns={columns} dataSource={demoAudit} pagination={false} />
    </section>
  );
}
