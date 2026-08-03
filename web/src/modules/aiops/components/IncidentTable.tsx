import { useQuery } from '@tanstack/react-query';
import { Alert, Select, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { Link, useSearchParams } from 'react-router-dom';

import { useAppStore } from '../../../stores/appStore';
import { CoreSpinLoader } from '../../../components/ui/core-spin-loader';
import { listIncidents } from '../api';
import { demoIncidents } from '../demo';
import { formatTime } from '../format';
import type { Incident, IncidentSeverity } from '../types';
import { IncidentStatusTag } from './IncidentStatusTag';

const severityColor: Record<IncidentSeverity, string> = {
  info: 'blue',
  warning: 'orange',
  critical: 'red',
};

const severityLabel: Record<IncidentSeverity, string> = {
  info: 'info',
  warning: 'warning',
  critical: 'critical',
};

const statusOptions = [
  { value: 'received', label: '已接收' },
  { value: 'triaging', label: '分类中' },
  { value: 'collecting', label: '采集中' },
  { value: 'analyzing', label: '分析中' },
  { value: 'proposing', label: '建议中' },
  { value: 'awaiting_approval', label: '待审批' },
  { value: 'approved', label: '已审批' },
  { value: 'executing', label: '执行中' },
  { value: 'resolved', label: '已解决' },
  { value: 'rejected', label: '已拒绝' },
  { value: 'failed', label: '失败' },
];

export function IncidentTable() {
  const dataMode = useAppStore((state) => state.dataMode);
  const [searchParams, setSearchParams] = useSearchParams();

  const status = searchParams.get('status') ?? undefined;
  const namespace = searchParams.get('namespace') ?? undefined;

  const incidentsQuery = useQuery({
    queryKey: ['aiops', 'incidents', { status, namespace }],
    queryFn: () => listIncidents({ status, namespace }),
    enabled: dataMode === 'live',
  });

  const incidents = dataMode === 'demo' ? demoIncidents : (incidentsQuery.data ?? []);
  const namespaces = Array.from(new Set(incidents.map((item) => item.namespace))).sort();

  const setFilter = (key: 'status' | 'namespace', value?: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) {
      next.set(key, value);
    } else {
      next.delete(key);
    }
    setSearchParams(next);
  };

  const columns: ColumnsType<Incident> = [
    {
      title: '摘要',
      dataIndex: 'summary',
      key: 'summary',
      render: (summary: string, record) => (
        <Link to={`/aiops/incidents/${record.id}`} className="font-semibold text-teal-700 hover:underline">
          {summary}
        </Link>
      ),
    },
    {
      title: '严重度',
      dataIndex: 'severity',
      key: 'severity',
      render: (severity: IncidentSeverity) => <Tag color={severityColor[severity]}>{severityLabel[severity]}</Tag>,
    },
    {
      title: '目标资源',
      key: 'target',
      render: (_, record) => (
        <Typography.Text className="font-mono text-xs">{`${record.resourceKind}/${record.resourceName}`}</Typography.Text>
      ),
    },
    { title: '命名空间', dataIndex: 'namespace', key: 'namespace' },
    {
      title: '阶段',
      dataIndex: 'status',
      key: 'status',
      render: (status: Incident['status']) => <IncidentStatusTag status={status} />,
    },
    {
      title: '更新时间',
      dataIndex: 'updatedAt',
      key: 'updatedAt',
      sorter: (left, right) => new Date(left.updatedAt).getTime() - new Date(right.updatedAt).getTime(),
      defaultSortOrder: 'descend',
      render: formatTime,
    },
    {
      title: '操作',
      key: 'action',
      render: (_, record) => <Link to={`/aiops/incidents/${record.id}`}>查看详情</Link>,
    },
  ];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-3">
        <Select
          allowClear
          placeholder="按阶段筛选"
          style={{ width: 160 }}
          options={statusOptions}
          value={status}
          onChange={(value) => setFilter('status', value)}
        />
        <Select
          allowClear
          placeholder="按命名空间筛选"
          style={{ width: 180 }}
          options={namespaces.map((ns) => ({ value: ns, label: ns }))}
          value={namespace}
          onChange={(value) => setFilter('namespace', value)}
        />
      </div>

      {incidentsQuery.isError ? (
        <Alert type="error" showIcon message="加载诊断任务失败" description={String(incidentsQuery.error)} />
      ) : null}

      {dataMode === 'live' && incidentsQuery.isLoading ? (
        <CoreSpinLoader minHeight="220px" />
      ) : (
        <Table<Incident>
          rowKey="id"
          columns={columns}
          dataSource={incidents}
          loading={false}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: '暂无诊断任务' }}
        />
      )}
    </div>
  );
}
