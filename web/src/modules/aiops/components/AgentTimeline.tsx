import { LoadingOutlined, CheckCircleFilled, CloseCircleFilled, MinusCircleOutlined } from '@ant-design/icons';
import { List, Tag, Typography } from 'antd';

import type { AgentRun } from '../types';

// 五阶段诊断角色，展示顺序固定。
const roleSteps = [
  { role: 'triage', label: '分类' },
  { role: 'collector', label: '证据采集' },
  { role: 'root_cause', label: '根因分析' },
  { role: 'remediation', label: '修复建议' },
  { role: 'risk_review', label: '风险评估' },
];

function latestRun(runs: AgentRun[], role: string): AgentRun | undefined {
  return runs
    .filter((run) => run.role === role)
    .sort((left, right) => right.attempt - left.attempt)[0];
}

function statusNode(run: AgentRun | undefined) {
  if (!run) {
    return <Tag icon={<MinusCircleOutlined />} color="default">等待</Tag>;
  }
  switch (run.status) {
    case 'running':
      return <Tag icon={<LoadingOutlined spin />} color="processing">进行中</Tag>;
    case 'succeeded':
      return <Tag icon={<CheckCircleFilled />} color="success">已完成</Tag>;
    case 'failed':
      return <Tag icon={<CloseCircleFilled />} color="error">失败</Tag>;
    case 'skipped':
      return <Tag color="warning">跳过</Tag>;
    default:
      return <Tag color="default">{run.status}</Tag>;
  }
}

// 摘要只展示 run 摘要与错误，不展示模型原始输出（避免泄漏隐藏推理）。
function detailNode(run: AgentRun | undefined) {
  if (!run) return <Typography.Text type="secondary">尚未开始</Typography.Text>;
  if (run.status === 'running') return <Typography.Text type="secondary">诊断进行中...</Typography.Text>;
  if (run.status === 'failed') {
    return <Typography.Text type="danger">{run.error || run.summary || '诊断失败'}</Typography.Text>;
  }
  return <Typography.Text type="secondary">{run.summary || '已完成'}</Typography.Text>;
}

export function AgentTimeline({ runs }: { runs: AgentRun[] }) {
  return (
    <List
      size="small"
      dataSource={roleSteps}
      renderItem={(step) => {
        const run = latestRun(runs, step.role);
        return (
          <List.Item>
            <List.Item.Meta
              title={
                <span className="flex items-center gap-2">
                  <Typography.Text strong>{step.label}</Typography.Text>
                  {statusNode(run)}
                </span>
              }
              description={detailNode(run)}
            />
          </List.Item>
        );
      }}
    />
  );
}
