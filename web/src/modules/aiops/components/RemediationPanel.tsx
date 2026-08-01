import { Empty, List, Tag, Typography } from 'antd';

import type { AgentRun } from '../types';

type RemediationAction = {
  command: string;
  reason: string;
  risk: 'low' | 'medium' | 'high';
};

type RemediationOutput = {
  actions: RemediationAction[];
};

const riskColor: Record<RemediationAction['risk'], string> = {
  low: 'green',
  medium: 'orange',
  high: 'red',
};

function parseRemediation(output: string): RemediationOutput {
  try {
    const parsed = JSON.parse(output) as RemediationOutput;
    if (!Array.isArray(parsed.actions)) {
      return { actions: [] };
    }
    return parsed;
  } catch {
    return { actions: [] };
  }
}

export function RemediationPanel({ run }: { run?: AgentRun }) {
  const output = run ? parseRemediation(run.output) : { actions: [] };
  if (!run || run.status !== 'succeeded' || output.actions.length === 0) {
    return <Empty description="暂无修复方案" image={Empty.PRESENTED_IMAGE_SIMPLE} />;
  }

  return (
    <List
      size="small"
      dataSource={output.actions}
      renderItem={(action, index) => (
        <List.Item>
          <div className="w-full">
            <div className="flex items-center justify-between gap-3">
              <Typography.Text strong>方案 {index + 1}</Typography.Text>
              <Tag color={riskColor[action.risk]}>风险 {action.risk}</Tag>
            </div>
            <Typography.Paragraph className="!mb-1 font-mono text-xs">
              {action.command}
            </Typography.Paragraph>
            <Typography.Paragraph type="secondary" className="!mb-0 text-sm">
              {action.reason}
            </Typography.Paragraph>
          </div>
        </List.Item>
      )}
    />
  );
}
