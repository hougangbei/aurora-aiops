import { useQuery } from '@tanstack/react-query';
import { Alert, Card, Col, Row, Statistic, Typography } from 'antd';

import { listIncidents } from '../modules/aiops/api';
import { demoIncidents } from '../modules/aiops/demo';
import { useAppStore } from '../stores/appStore';

const terminalStatuses = new Set(['resolved', 'rejected', 'failed']);

function percentage(part: number, total: number): string {
  if (total === 0) return '--';
  return `${((part / total) * 100).toFixed(1)}%`;
}

export function AIOpsOverviewPage() {
  const dataMode = useAppStore((state) => state.dataMode);

  const incidentsQuery = useQuery({
    queryKey: ['aiops', 'incidents'],
    queryFn: () => listIncidents(),
    enabled: dataMode === 'live',
  });

  const incidents = dataMode === 'demo' ? demoIncidents : (incidentsQuery.data ?? []);

  const active = incidents.filter((item) => !terminalStatuses.has(item.status)).length;
  const awaitingApproval = incidents.filter((item) => item.status === 'awaiting_approval').length;
  const critical = incidents.filter((item) => item.severity === 'critical').length;

  const now = Date.now();
  const oneDayAgo = now - 24 * 60 * 60 * 1000;
  const last24h = incidents.filter((item) => new Date(item.updatedAt).getTime() >= oneDayAgo);
  const resolved24h = last24h.filter((item) => item.status === 'resolved').length;
  const failed24h = last24h.filter((item) => item.status === 'failed').length;

  return (
    <section className="space-y-4 rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <div>
        <Typography.Title level={2} className="!mb-1">
          智能运维 · 工作台
        </Typography.Title>
        <Typography.Text type="secondary">
          诊断工作流指标总览。数据不足的指标显示 `--`，不伪造趋势。
        </Typography.Text>
      </div>

      {incidentsQuery.isError ? (
        <Alert type="error" showIcon message="加载诊断任务失败" description={String(incidentsQuery.error)} />
      ) : null}

      <Row gutter={[16, 16]}>
        <Col xs={12} md={8} xl={4}>
          <Card bordered={false} className="rounded-2xl bg-slate-50">
            <Statistic title="活跃诊断" value={incidents.length === 0 ? '--' : active} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card bordered={false} className="rounded-2xl bg-slate-50">
            <Statistic title="待审批" value={incidents.length === 0 ? '--' : awaitingApproval} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card bordered={false} className="rounded-2xl bg-slate-50">
            <Statistic title="严重告警" value={incidents.length === 0 ? '--' : critical} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card bordered={false} className="rounded-2xl bg-slate-50">
            <Statistic
              title="近 24h 成功率"
              value={last24h.length === 0 ? '--' : percentage(resolved24h, resolved24h + failed24h)}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card bordered={false} className="rounded-2xl bg-slate-50">
            <Statistic title="平均 MTTD" value="--" />
          </Card>
        </Col>
      </Row>
    </section>
  );
}
