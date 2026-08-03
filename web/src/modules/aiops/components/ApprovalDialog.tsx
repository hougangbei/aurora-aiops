import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Button, Empty, Form, Input, Modal, Space, Spin, Tag, Typography } from 'antd';
import { useMemo, useState } from 'react';

import { approveRemediation, getRuns, rejectRemediation } from '../api';
import type { AgentRun, EffectiveRiskReview } from '../types';

type RemediationAction = {
  command: string;
  reason: string;
};

function parseActions(output: string): RemediationAction[] {
  try {
    const parsed = JSON.parse(output) as { actions?: RemediationAction[] };
    return Array.isArray(parsed.actions) ? parsed.actions : [];
  } catch {
    return [];
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string');
}

function isRisk(value: unknown): value is 'low' | 'medium' | 'high' {
  return value === 'low' || value === 'medium' || value === 'high';
}

function isModelRisk(value: unknown): value is 'low' | 'medium' | 'high' | 'critical' {
  return isRisk(value) || value === 'critical';
}

function isPolicyActionReview(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.kind === 'string' &&
    typeof value.resourceKind === 'string' &&
    typeof value.resourceName === 'string' &&
    typeof value.allowed === 'boolean' &&
    isRisk(value.risk) &&
    typeof value.reason === 'string'
  );
}

function isModelReview(value: unknown): boolean {
  return (
    isRecord(value) &&
    isModelRisk(value.riskLevel) &&
    typeof value.approved === 'boolean' &&
    (value.blockers === undefined || isStringArray(value.blockers)) &&
    (value.rationale === undefined || typeof value.rationale === 'string')
  );
}

// parseEffectiveReview decodes the risk_review run output into the authoritative
// effective review. It returns undefined on missing output, invalid JSON, or
// invalid field shapes so the dialog fails closed instead of trusting bad data.
function parseEffectiveReview(output: string | undefined): EffectiveRiskReview | undefined {
  if (!output) {
    return undefined;
  }
  try {
    const parsed: unknown = JSON.parse(output);
    if (
      !isRecord(parsed) ||
      !isRisk(parsed.effectiveRisk) ||
      typeof parsed.approvable !== 'boolean' ||
      (parsed.blockers !== undefined && !isStringArray(parsed.blockers)) ||
      !Array.isArray(parsed.actions) ||
      !parsed.actions.every(isPolicyActionReview) ||
      (parsed.approvable && parsed.actions.length === 0) ||
      !isModelReview(parsed.modelReview)
    ) {
      return undefined;
    }
    return parsed as EffectiveRiskReview;
  } catch {
    return undefined;
  }
}

function latestSucceededRun(runs: AgentRun[], role: string): AgentRun | undefined {
  return runs
    .filter((run) => run.role === role && run.status === 'succeeded')
    .sort((a, b) => b.attempt - a.attempt)[0];
}

const effectiveRiskColor: Record<EffectiveRiskReview['effectiveRisk'], string> = {
  low: 'green',
  medium: 'orange',
  high: 'red',
};

const MIN_REASON_LENGTH = 8;

export function ApprovalDialog({
  incidentId,
  open,
  onClose,
}: {
  incidentId: string;
  open: boolean;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const { message } = App.useApp();
  const [reason, setReason] = useState('');

  // 每次打开时清空上一次的审批理由。
  const [lastOpen, setLastOpen] = useState(open);
  if (open !== lastOpen) {
    setLastOpen(open);
    if (open) setReason('');
  }

  const runsQuery = useQuery({
    queryKey: ['aiops', 'runs', incidentId],
    queryFn: () => getRuns(incidentId),
    enabled: open && Boolean(incidentId),
  });

  const runs = runsQuery.data?.runs ?? [];

  // Remediation parsing is for command/reason display only; it never drives
  // approval permission. The authoritative gate is the effective risk review.
  const actions = useMemo(() => {
    const run = latestSucceededRun(runs, 'remediation');
    return run ? parseActions(run.output) : [];
  }, [runs]);

  const review = useMemo(() => {
    const run = latestSucceededRun(runs, 'risk_review');
    return run ? parseEffectiveReview(run.output) : undefined;
  }, [runs]);

  const canApprove = review?.approvable === true;
  const reasonReady = reason.trim().length >= MIN_REASON_LENGTH;

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['aiops', 'incidents'] });
    void queryClient.invalidateQueries({ queryKey: ['aiops', 'runs', incidentId] });
    setReason('');
    onClose();
  };

  const approveMutation = useMutation({
    mutationFn: () => approveRemediation(incidentId, reason.trim()),
    onSuccess: () => {
      message.success('已批准修复方案');
      refresh();
    },
    onError: (error) => message.error(`批准失败：${String(error)}`),
  });

  const rejectMutation = useMutation({
    mutationFn: () => rejectRemediation(incidentId, reason.trim()),
    onSuccess: () => {
      message.success('已拒绝修复方案');
      refresh();
    },
    onError: (error) => message.error(`拒绝失败：${String(error)}`),
  });

  const busy = approveMutation.isPending || rejectMutation.isPending;

  return (
    <Modal title="修复方案审批" open={open} onCancel={onClose} footer={null} width={520}>
      {runsQuery.isLoading ? (
        <div className="flex justify-center py-8">
          <Spin />
        </div>
      ) : actions.length === 0 ? (
        <Empty description="暂无可审批的修复方案" />
      ) : (
        <div className="space-y-4">
          <div className="space-y-2">
            {actions.map((action, index) => (
              <div key={index} className="rounded-lg border border-slate-200 bg-slate-50 p-3">
                <Typography.Text strong>方案 {index + 1}</Typography.Text>
                <Typography.Paragraph className="!mb-1 font-mono text-xs">{action.command}</Typography.Paragraph>
                <Typography.Paragraph type="secondary" className="!mb-0 text-sm">
                  {action.reason}
                </Typography.Paragraph>
              </div>
            ))}
          </div>

          {review ? (
            <div className="space-y-2">
              <div className="flex items-center gap-2">
                <Typography.Text type="secondary">有效风险</Typography.Text>
                <Tag color={effectiveRiskColor[review.effectiveRisk]}>{review.effectiveRisk}</Tag>
              </div>
              {review.blockers && review.blockers.length > 0 ? (
                <Alert type={canApprove ? 'info' : 'warning'} showIcon message={review.blockers.join('；')} />
              ) : null}
            </div>
          ) : (
            <Alert type="error" showIcon message="有效风险评审缺失，无法批准该方案。" />
          )}

          <Form layout="vertical">
            <Form.Item
              label="审批理由"
              help={`至少 ${MIN_REASON_LENGTH} 个字符，用于审计记录。`}
            >
              <Input.TextArea
                rows={3}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                placeholder="说明批准或拒绝的理由"
              />
            </Form.Item>
          </Form>

          <Space>
            {canApprove ? (
              <Button
                type="primary"
                disabled={!reasonReady || busy}
                loading={approveMutation.isPending}
                onClick={() => approveMutation.mutate()}
              >
                批准执行
              </Button>
            ) : null}
            <Button
              danger
              disabled={!reasonReady || busy}
              loading={rejectMutation.isPending}
              onClick={() => rejectMutation.mutate()}
            >
              拒绝
            </Button>
          </Space>
        </div>
      )}
    </Modal>
  );
}
