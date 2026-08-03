import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMemo } from 'react';

import { getExperimentMetrics } from '../modules/aiops/api';
import { useAppStore } from '../stores/appStore';
import type { ExperimentMetrics } from '../modules/aiops/types';
import { CoreSpinLoader } from '../components/ui/core-spin-loader';

const groupLabel: Record<string, string> = {
  rules: '规则基线',
  single_llm: '单 LLM',
  multi_agent: '多智能体',
};

function percent(value: number, insufficient: boolean): string {
  if (insufficient) return '样本不足';
  return `${(value * 100).toFixed(1)}%`;
}

export function AIOpsExperimentsPage() {
  const dataMode = useAppStore((state) => state.dataMode);

  const metricsQuery = useQuery({
    queryKey: ['aiops', 'experiments', 'metrics'],
    queryFn: () => getExperimentMetrics(),
    enabled: dataMode === 'live',
  });

  const metrics = useMemo<ExperimentMetrics[]>(() => metricsQuery.data ?? [], [metricsQuery.data]);
  const insufficient = metrics.some((m) => m.insufficientSamples);

  const columns: ColumnsType<ExperimentMetrics> = [
    { title: '分组', dataIndex: 'group', key: 'group', render: (g: string) => groupLabel[g] ?? g },
    { title: '样本数', dataIndex: 'sampleCount', key: 'sampleCount', sorter: (a, b) => a.sampleCount - b.sampleCount },
    { title: 'Top-1', dataIndex: 'top1Rate', key: 'top1Rate', render: (v: number, m) => percent(v, m.insufficientSamples) },
    { title: 'Top-3', dataIndex: 'top3Rate', key: 'top3Rate', render: (v: number, m) => percent(v, m.insufficientSamples) },
    { title: 'MTTD (s)', dataIndex: 'avgMttdSeconds', key: 'avgMttdSeconds', render: (v: number) => v.toFixed(1) },
    { title: '证据完整率', dataIndex: 'evidenceCompletenessRate', key: 'evidenceCompletenessRate', render: (v: number, m) => percent(v, m.insufficientSamples) },
    { title: '高危拦截率', dataIndex: 'highRiskInterceptionRate', key: 'highRiskInterceptionRate', render: (v: number, m) => percent(v, m.insufficientSamples) },
    { title: '平均 Token', dataIndex: 'avgTokens', key: 'avgTokens' },
    {
      title: 'Top-1 置信区间',
      key: 'ci',
      render: (_, m) => (m.insufficientSamples ? '—' : `±${(m.confidenceInterval * 100).toFixed(1)}%`),
    },
    {
      title: '样本状态',
      key: 'status',
      render: (_, m) =>
        m.insufficientSamples ? <Tag color="warning">样本不足（&lt;30）</Tag> : <Tag color="green">足够</Tag>,
    },
  ];

  return (
    <section className="space-y-4 rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <div className="flex items-center justify-between gap-4">
        <div>
          <Typography.Title level={2} className="!mb-1">
            对比实验
          </Typography.Title>
          <Typography.Text type="secondary">
            固定分组 rules / single_llm / multi_agent，同一 run seed 保持一致；样本不足时明确标记，不展示虚假百分比。
          </Typography.Text>
        </div>
        <Button
          size="small"
          disabled={dataMode !== 'live'}
          onClick={() => window.open('/api/v1/experiments/metrics?format=csv', '_blank')}
        >
          导出 CSV
        </Button>
      </div>

      {insufficient ? (
        <Alert
          type="warning"
          showIcon
          message="部分分组样本数不足 30，置信区间与百分比仅供参考，不用于结论。"
        />
      ) : null}

      {metricsQuery.isError ? (
        <Alert type="error" showIcon message="加载实验结果失败" description={String(metricsQuery.error)} />
      ) : null}

      {dataMode === 'demo' ? (
        <Alert type="info" showIcon message="演示模式展示实验指标结构，实际数据来自真实实验运行。" />
      ) : metricsQuery.isLoading ? (
        <CoreSpinLoader minHeight="220px" />
      ) : (
        <Table<ExperimentMetrics>
          rowKey="group"
          columns={columns}
          dataSource={metrics}
          loading={false}
          pagination={false}
          locale={{ emptyText: '暂无实验数据' }}
        />
      )}
    </section>
  );
}
