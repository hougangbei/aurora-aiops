import {
  AlertOutlined,
  ArrowRightOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  DeploymentUnitOutlined,
  ExperimentOutlined,
  PlusOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  WarningFilled,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Col, Row, Space, Statistic, Tag, Typography } from 'antd';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

import { CoreSpinLoader } from '../components/ui/core-spin-loader';
import { getAIOpsReadiness, getClusterConnection, listIncidents } from '../modules/aiops/api';
import { IncidentStatusTag } from '../modules/aiops/components/IncidentStatusTag';
import { demoIncidents } from '../modules/aiops/demo';
import { formatTime } from '../modules/aiops/format';
import type { AIOpsReadiness, ClusterConnection, Incident } from '../modules/aiops/types';
import { useAppStore } from '../stores/appStore';

const terminalStatuses = new Set(['resolved', 'rejected', 'failed']);

const demoReadiness: AIOpsReadiness = {
  modelConfigured: true,
  model: 'demo-model',
  configurationSource: 'environment',
  runtimeMutable: false,
  deterministicRolesAvailable: true,
  remediationAvailable: true,
};

const demoConnection: ClusterConnection = {
  state: 'connected',
  version: 'demo',
  latencyMs: 12,
  capabilities: { nodes: true, events: true, podLogs: true, metrics: true },
};

function percentage(part: number, total: number): string {
  if (total === 0) return '--';
  return `${((part / total) * 100).toFixed(1)}%`;
}

function ReadinessCard({
  icon,
  title,
  status,
  detail,
  tone,
}: {
  icon: ReactNode;
  title: string;
  status: string;
  detail: string;
  tone: 'success' | 'warning' | 'error' | 'info';
}) {
  const statusIcon =
    tone === 'success' ? (
      <CheckCircleFilled className="text-emerald-400" />
    ) : tone === 'error' ? (
      <WarningFilled className="text-rose-400" />
    ) : tone === 'warning' ? (
      <WarningFilled className="text-amber-400" />
    ) : (
      <CheckCircleFilled className="text-sky-400" />
    );

  return (
    <Card size="small" className="h-full rounded-[18px]">
      <div className="flex items-start justify-between gap-3">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-indigo-300/20 bg-indigo-400/10 text-lg text-indigo-200">
          {icon}
        </div>
        {statusIcon}
      </div>
      <Typography.Text type="secondary" className="mt-4 block text-xs uppercase tracking-[0.14em]">
        {title}
      </Typography.Text>
      <Typography.Text strong className="mt-1 block text-base">
        {status}
      </Typography.Text>
      <Typography.Text type="secondary" className="mt-1 block text-xs">
        {detail}
      </Typography.Text>
    </Card>
  );
}

function attentionLabel(incident: Incident, modelConfigured: boolean) {
  if (!modelConfigured && incident.status === 'collecting') {
    return <Tag color="warning">等待模型</Tag>;
  }
  return <IncidentStatusTag status={incident.status} />;
}

export function AIOpsOverviewPage() {
  const dataMode = useAppStore((state) => state.dataMode);

  const incidentsQuery = useQuery({
    queryKey: ['aiops', 'incidents'],
    queryFn: () => listIncidents(),
    enabled: dataMode === 'live',
  });
  const readinessQuery = useQuery({
    queryKey: ['aiops', 'readiness'],
    queryFn: getAIOpsReadiness,
    enabled: dataMode === 'live',
  });
  const connectionQuery = useQuery({
    queryKey: ['cluster', 'connection'],
    queryFn: getClusterConnection,
    enabled: dataMode === 'live',
  });

  const incidents = dataMode === 'demo' ? demoIncidents : (incidentsQuery.data ?? []);
  const readiness = dataMode === 'demo' ? demoReadiness : readinessQuery.data;
  const connection = dataMode === 'demo' ? demoConnection : connectionQuery.data;

  const active = incidents.filter((item) => !terminalStatuses.has(item.status));
  const awaitingApproval = incidents.filter((item) => item.status === 'awaiting_approval').length;
  const critical = active.filter((item) => item.severity === 'critical').length;
  const waitingModel = readiness?.modelConfigured === false
    ? incidents.filter((item) => item.status === 'collecting').length
    : 0;

  const oneDayAgo = Date.now() - 24 * 60 * 60 * 1000;
  const last24h = incidents.filter((item) => new Date(item.updatedAt).getTime() >= oneDayAgo);
  const resolved24h = last24h.filter((item) => item.status === 'resolved').length;
  const failed24h = last24h.filter((item) => item.status === 'failed').length;
  const attention = active.slice(0, 5);

  const connectionTone = connection?.state === 'connected' ? 'success' : connection?.state === 'unreachable' ? 'error' : 'warning';
  const connectionStatus = connection?.state === 'connected'
    ? '集群已连接'
    : connection?.state === 'unreachable'
      ? '集群不可达'
      : '集群部分降级';
  const connectionDetail = connection
    ? connection.capabilities.metrics
      ? `Kubernetes ${connection.version} · ${connection.latencyMs}ms`
      : 'Metrics 不可用'
    : '正在检查连接状态';

  if (dataMode === 'live' && incidentsQuery.isLoading) {
    return <CoreSpinLoader minHeight="360px" />;
  }

  return (
    <section className="space-y-5">
      <div className="aurora-panel rounded-[24px] border p-6">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="max-w-3xl">
            <Typography.Text className="aurora-eyebrow text-xs font-semibold uppercase tracking-[0.2em]">
              Aurora Operations Center
            </Typography.Text>
            <Typography.Title level={2} className="!mb-1 !mt-2">
              事件指挥台
            </Typography.Title>
            <Typography.Text type="secondary">
              先看系统是否具备诊断条件，再处理阻塞、审批和故障。所有状态均来自真实运行数据。
            </Typography.Text>
          </div>
          <Space wrap>
            <Link to="/aiops/incidents">
              <Button icon={<PlusOutlined />} type="primary">创建诊断任务</Button>
            </Link>
            <Link to="/aiops/approvals">
              <Button icon={<SafetyCertificateOutlined />}>待审批 {awaitingApproval}</Button>
            </Link>
          </Space>
        </div>

        {(incidentsQuery.isError || readinessQuery.isError || connectionQuery.isError) && dataMode === 'live' ? (
          <Alert
            className="mt-5"
            type="error"
            showIcon
            message="部分运行状态加载失败"
            description="事件列表、集群连接或智能运维能力状态暂时不可用，请刷新后重试。"
          />
        ) : null}

        <Row gutter={[14, 14]} className="mt-5">
          <Col xs={24} sm={12} xl={6}>
            <ReadinessCard
              icon={<CloudServerOutlined />}
              title="Kubernetes"
              status={connectionStatus}
              detail={connectionDetail}
              tone={connectionTone}
            />
          </Col>
          <Col xs={24} sm={12} xl={6}>
            <ReadinessCard
              icon={<ExperimentOutlined />}
              title="证据采集"
              status={readiness?.deterministicRolesAvailable ? '基础诊断可用' : '诊断引擎未就绪'}
              detail="分类、事件、日志和资源快照"
              tone={readiness?.deterministicRolesAvailable ? 'success' : 'error'}
            />
          </Col>
          <Col xs={24} sm={12} xl={6}>
            <ReadinessCard
              icon={<RobotOutlined />}
              title="AI 根因分析"
              status={readiness?.modelConfigured ? readiness.model || '模型已连接' : '模型未配置'}
              detail={readiness?.modelConfigured ? '根因、建议和风险评估可用' : '基础证据仍会保留，模型阶段暂停'}
              tone={readiness?.modelConfigured ? 'success' : 'warning'}
            />
          </Col>
          <Col xs={24} sm={12} xl={6}>
            <ReadinessCard
              icon={<SafetyCertificateOutlined />}
              title="修复护栏"
              status={readiness?.remediationAvailable ? '审批保护已启用' : '修复执行未接入'}
              detail="风险策略优先于模型建议"
              tone={readiness?.remediationAvailable ? 'success' : 'info'}
            />
          </Col>
        </Row>
      </div>

      <Row gutter={[16, 16]}>
        <Col xs={12} md={8} xl={5}>
          <Card className="h-full rounded-[18px]">
            <Statistic title="活跃诊断" value={incidents.length === 0 ? '--' : active.length} prefix={<DeploymentUnitOutlined />} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={5}>
          <Card className="h-full rounded-[18px]">
            <Statistic title="等待模型" value={incidents.length === 0 ? '--' : waitingModel} prefix={<RobotOutlined />} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={5}>
          <Card className="h-full rounded-[18px]">
            <Statistic title="待审批" value={incidents.length === 0 ? '--' : awaitingApproval} prefix={<SafetyCertificateOutlined />} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={5}>
          <Card className="h-full rounded-[18px]">
            <Statistic title="严重事件" value={incidents.length === 0 ? '--' : critical} prefix={<AlertOutlined />} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card className="h-full rounded-[18px]">
            <Statistic title="24h 闭环率" value={percentage(resolved24h, resolved24h + failed24h)} />
          </Card>
        </Col>
      </Row>

      <div className="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1.65fr)_minmax(300px,0.75fr)]">
        <Card
          title="需要处理"
          className="rounded-[20px]"
          extra={<Link to="/aiops/incidents">查看全部 <ArrowRightOutlined /></Link>}
        >
          {attention.length === 0 ? (
            <div className="py-10 text-center">
              <CheckCircleFilled className="text-3xl text-emerald-400" />
              <Typography.Text className="mt-3 block">当前没有活跃诊断任务</Typography.Text>
            </div>
          ) : (
            <div className="divide-y divide-indigo-200/10">
              {attention.map((incident) => (
                <Link
                  key={incident.id}
                  to={`/aiops/incidents/${incident.id}`}
                  className="flex flex-col gap-3 py-4 text-inherit first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <Typography.Text strong className="truncate">{incident.summary}</Typography.Text>
                      <Tag color={incident.severity === 'critical' ? 'red' : incident.severity === 'warning' ? 'orange' : 'blue'}>
                        {incident.severity}
                      </Tag>
                    </div>
                    <Typography.Text type="secondary" className="mt-1 block text-xs">
                      {incident.namespace}/{incident.resourceKind}/{incident.resourceName} · {formatTime(incident.updatedAt)}
                    </Typography.Text>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {attentionLabel(incident, readiness?.modelConfigured ?? false)}
                    <ArrowRightOutlined className="text-indigo-300" />
                  </div>
                </Link>
              ))}
            </div>
          )}
        </Card>

        <Card title="下一步" className="rounded-[20px]">
          <div className="space-y-3">
            {!readiness?.modelConfigured ? (
              <div className="rounded-[16px] border border-amber-300/20 bg-amber-300/5 p-4">
                <div className="flex items-center gap-2 font-semibold text-amber-200">
                  <WarningFilled /> 模型链路未启用
                </div>
                <Typography.Text type="secondary" className="mt-2 block text-sm">
                  当前只能完成分类和证据采集，无法生成根因与修复建议。
                </Typography.Text>
                <Link to="/aiops/settings" className="mt-3 inline-block font-medium">
                  配置模型 <ArrowRightOutlined />
                </Link>
              </div>
            ) : null}
            {!connection?.capabilities.metrics ? (
              <div className="rounded-[16px] border border-sky-300/20 bg-sky-300/5 p-4">
                <div className="font-semibold text-sky-200">补齐 Metrics 能力</div>
                <Typography.Text type="secondary" className="mt-2 block text-sm">
                  日志和事件仍可采集，但 CPU、内存证据会缺失。
                </Typography.Text>
              </div>
            ) : null}
            <Link to="/aiops/settings">
              <Button block icon={<SettingOutlined />}>运行能力与配置</Button>
            </Link>
          </div>
        </Card>
      </div>
    </section>
  );
}
