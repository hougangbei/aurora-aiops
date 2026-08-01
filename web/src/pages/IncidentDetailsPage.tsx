import { ReloadOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Button, Card, Descriptions, Space, Spin, Tag, Typography } from 'antd';
import { useMemo } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { AgentTimeline } from '../modules/aiops/components/AgentTimeline';
import { IncidentStatusTag } from '../modules/aiops/components/IncidentStatusTag';
import { RemediationPanel } from '../modules/aiops/components/RemediationPanel';
import { RootCausePanel } from '../modules/aiops/components/RootCausePanel';
import { getEvidence, getIncident, getRuns, reanalyzeIncident } from '../modules/aiops/api';
import { demoIncidents } from '../modules/aiops/demo';
import { formatTime } from '../modules/aiops/format';
import type { AgentRun } from '../modules/aiops/types';
import { useIncidentEvents } from '../modules/aiops/useIncidentEvents';
import { useAppStore } from '../stores/appStore';

const severityColor: Record<string, string> = { info: 'blue', warning: 'orange', critical: 'red' };

export function IncidentDetailsPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const dataMode = useAppStore((state) => state.dataMode);
  const queryClient = useQueryClient();
  const { modal, message } = App.useApp();

  const incidentQuery = useQuery({
    queryKey: ['aiops', 'incidents', id],
    queryFn: () => getIncident(id),
    enabled: dataMode === 'live' && Boolean(id),
  });
  const runsQuery = useQuery({
    queryKey: ['aiops', 'runs', id],
    queryFn: () => getRuns(id),
    enabled: dataMode === 'live' && Boolean(id),
    select: (data) => data.runs,
  });
  const evidenceQuery = useQuery({
    queryKey: ['aiops', 'evidence', id],
    queryFn: () => getEvidence(id),
    enabled: dataMode === 'live' && Boolean(id),
  });

  // SSE 增量更新：收到事件后刷新上面的查询缓存。
  useIncidentEvents({ incidentId: id, enabled: dataMode === 'live' && Boolean(id) });

  const incident = dataMode === 'demo' ? demoIncidents.find((item) => item.id === id) : incidentQuery.data;
  const runs = dataMode === 'demo' ? ([] as AgentRun[]) : (runsQuery.data ?? []);

  const rootCauseRun = useMemo(
    () => runs.filter((run) => run.role === 'root_cause').sort((a, b) => b.attempt - a.attempt)[0],
    [runs],
  );
  const remediationRun = useMemo(
    () => runs.filter((run) => run.role === 'remediation').sort((a, b) => b.attempt - a.attempt)[0],
    [runs],
  );

  const reanalyzeMutation = useMutation({
    mutationFn: () => reanalyzeIncident(id),
    onSuccess: () => {
      message.success('已重新触发诊断');
      void queryClient.invalidateQueries({ queryKey: ['aiops', 'incidents', id] });
      void queryClient.invalidateQueries({ queryKey: ['aiops', 'runs', id] });
    },
    onError: (error) => {
      message.error(`重新诊断失败：${String(error)}`);
    },
  });

  const confirmReanalyze = () => {
    modal.confirm({
      title: '重新诊断',
      content: '将清空该 Incident 的既有证据并从头重跑五阶段诊断，确定继续吗？',
      okText: '重新诊断',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: () => reanalyzeMutation.mutate(),
    });
  };

  if (dataMode === 'live' && incidentQuery.isLoading) {
    return (
      <div className="flex justify-center py-24">
        <Spin size="large" />
      </div>
    );
  }

  if (!incident) {
    return <Alert type="warning" showIcon message="Incident 不存在" />;
  }

  return (
    <section className="space-y-4 rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      {incidentQuery.isError ? (
        <Alert type="error" showIcon message="加载失败" description={String(incidentQuery.error)} />
      ) : null}

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <Typography.Title level={2} className="!mb-0">
              {incident.summary}
            </Typography.Title>
            <IncidentStatusTag status={incident.status} />
          </div>
          <Descriptions
            className="!mt-4"
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'severity', label: '严重度', children: <Tag color={severityColor[incident.severity]}>{incident.severity}</Tag> },
              { key: 'target', label: '目标资源', children: `${incident.namespace}/${incident.resourceKind}/${incident.resourceName}` },
              { key: 'created', label: '创建时间', children: formatTime(incident.createdAt) },
              { key: 'updated', label: '更新时间', children: formatTime(incident.updatedAt) },
            ]}
          />
        </div>
        <Space>
          {dataMode === 'live' ? (
            <Button
              icon={<ReloadOutlined />}
              loading={reanalyzeMutation.isPending}
              disabled={reanalyzeMutation.isPending}
              onClick={confirmReanalyze}
            >
              重新诊断
            </Button>
          ) : null}
        </Space>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card title="诊断阶段" size="small">
          <AgentTimeline runs={runs} />
        </Card>
        <Card title="根因候选与证据引用" size="small">
          <RootCausePanel run={rootCauseRun} />
        </Card>
      </div>

      <Card title="修复方案与风险" size="small">
        <RemediationPanel run={remediationRun} />
      </Card>

      <Card
        title="证据图"
        size="small"
        extra={
          <Button size="small" type="link" onClick={() => navigate(`/aiops/incidents/${id}/evidence`)}>
            查看证据图 →
          </Button>
        }
      >
        {dataMode === 'demo' ? (
          <Typography.Text type="secondary">演示模式不展示证据图。</Typography.Text>
        ) : evidenceQuery.isLoading ? (
          <Spin />
        ) : (
          <Typography.Text type="secondary">
            {evidenceQuery.data ? `${evidenceQuery.data.nodes.length} 节点 / ${evidenceQuery.data.edges.length} 边` : '暂无证据'}
          </Typography.Text>
        )}
      </Card>
    </section>
  );
}
