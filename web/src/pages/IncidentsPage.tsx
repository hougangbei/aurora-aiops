import { PlusOutlined } from '@ant-design/icons';
import { Button, Space, Typography } from 'antd';
import { useState } from 'react';

import { CreateIncidentModal } from '../modules/aiops/components/CreateIncidentModal';
import { IncidentTable } from '../modules/aiops/components/IncidentTable';
import { useAppStore } from '../stores/appStore';

export function IncidentsPage() {
  const dataMode = useAppStore((state) => state.dataMode);
  const [createOpen, setCreateOpen] = useState(false);

  return (
    <section className="space-y-4 rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <div className="flex items-center justify-between gap-4">
        <div>
          <Typography.Title level={2} className="!mb-1">
            Incident 列表
          </Typography.Title>
          <Typography.Text type="secondary">诊断任务列表、创建与筛选，按更新时间倒序。</Typography.Text>
        </div>
        <Space>
          {dataMode === 'live' ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              创建诊断任务
            </Button>
          ) : null}
        </Space>
      </div>

      <IncidentTable />
      <CreateIncidentModal open={createOpen} onClose={() => setCreateOpen(false)} />
    </section>
  );
}
