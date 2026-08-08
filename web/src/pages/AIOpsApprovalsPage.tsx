import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useState } from 'react';

import { ApprovalDialog } from '../modules/aiops/components/ApprovalDialog';
import { IncidentStatusTag } from '../modules/aiops/components/IncidentStatusTag';
import { listIncidents } from '../modules/aiops/api';
import { demoIncidents } from '../modules/aiops/demo';
import { useAppStore } from '../stores/appStore';
import type { Incident } from '../modules/aiops/types';
import { CoreSpinLoader } from '../components/ui/core-spin-loader';

export function AIOpsApprovalsPage() {
  const dataMode = useAppStore((state) => state.dataMode);
  const [activeId, setActiveId] = useState<string>();

  const approvalsQuery = useQuery({
    queryKey: ['aiops', 'incidents', { status: 'awaiting_approval' }],
    queryFn: () => listIncidents({ status: 'awaiting_approval' }),
    enabled: dataMode === 'live',
  });

  const incidents =
    dataMode === 'demo'
      ? demoIncidents.filter((item) => item.status === 'awaiting_approval')
      : (approvalsQuery.data ?? []);

  const columns: ColumnsType<Incident> = [
    { title: '摘要', dataIndex: 'summary', key: 'summary' },
    { title: '目标资源', key: 'target', render: (_, r) => `${r.namespace}/${r.resourceKind}/${r.resourceName}` },
    { title: '严重度', dataIndex: 'severity', key: 'severity' },
    { title: '阶段', dataIndex: 'status', key: 'status', render: (s: Incident['status']) => <IncidentStatusTag status={s} /> },
    {
      title: '操作',
      key: 'action',
      render: (_, record) => (
        <Button type="primary" size="small" onClick={() => setActiveId(record.id)}>
          处理审批
        </Button>
      ),
    },
  ];

  return (
    <section className="aurora-panel space-y-4 rounded-[24px] border p-6">
      <div>
        <Typography.Title level={2} className="!mb-1">
          修复审批
        </Typography.Title>
        <Typography.Text type="secondary">高风险方案只能拒绝；中低风险批准需填写至少 8 字理由。</Typography.Text>
      </div>

      {approvalsQuery.isError ? (
        <Alert type="error" showIcon message="加载待审批列表失败" description={String(approvalsQuery.error)} />
      ) : null}

      {dataMode === 'live' && approvalsQuery.isLoading ? (
        <CoreSpinLoader minHeight="220px" />
      ) : (
        <Table<Incident>
          rowKey="id"
          columns={columns}
          dataSource={incidents}
          loading={false}
          pagination={false}
          locale={{ emptyText: '暂无待审批任务' }}
        />
      )}

      <ApprovalDialog incidentId={activeId ?? ''} open={Boolean(activeId)} onClose={() => setActiveId(undefined)} />
    </section>
  );
}
