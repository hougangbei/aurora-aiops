import { Empty, List, Progress, Tag, Typography } from 'antd';

import type { AgentRun } from '../types';

type RootCauseCandidate = {
  summary: string;
  confidence: number;
  evidenceIds: string[];
  verificationSteps?: string[];
};

type RootCauseOutput = {
  candidates: RootCauseCandidate[];
};

function parseRootCause(output: string): RootCauseOutput {
  try {
    const parsed = JSON.parse(output) as RootCauseOutput;
    if (!Array.isArray(parsed.candidates)) {
      return { candidates: [] };
    }
    return parsed;
  } catch {
    return { candidates: [] };
  }
}

export function RootCausePanel({ run }: { run?: AgentRun }) {
  const output = run ? parseRootCause(run.output) : { candidates: [] };
  if (!run || run.status !== 'succeeded' || output.candidates.length === 0) {
    return <Empty description="暂无根因候选" image={Empty.PRESENTED_IMAGE_SIMPLE} />;
  }

  return (
    <List
      size="small"
      dataSource={output.candidates}
      renderItem={(candidate, index) => (
        <List.Item>
          <div className="w-full">
            <div className="flex items-center justify-between gap-3">
              <Typography.Text strong>候选 {index + 1}</Typography.Text>
              <span className="w-40">
                <Progress
                  percent={Math.round(candidate.confidence * 100)}
                  size="small"
                  status={candidate.confidence >= 0.8 ? 'success' : 'active'}
                />
              </span>
            </div>
            <Typography.Paragraph className="!mb-1">{candidate.summary}</Typography.Paragraph>
            <div className="flex flex-wrap gap-1">
              {candidate.evidenceIds.map((id) => (
                <Tag key={id} color="geekblue" className="font-mono text-xs">
                  {id}
                </Tag>
              ))}
            </div>
            {candidate.verificationSteps && candidate.verificationSteps.length > 0 ? (
              <Typography.Paragraph type="secondary" className="!mb-0 !mt-2 text-xs">
                验证步骤：{candidate.verificationSteps.join(' → ')}
              </Typography.Paragraph>
            ) : null}
          </div>
        </List.Item>
      )}
    />
  );
}
