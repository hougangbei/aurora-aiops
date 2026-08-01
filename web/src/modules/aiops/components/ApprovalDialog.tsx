import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Button, Empty, Form, Input, Modal, Space, Spin, Tag, Typography } from 'antd';
import { useMemo, useState } from 'react';

import { approveRemediation, getRuns, rejectRemediation } from '../api';
import type { AgentRun } from '../types';

type RemediationAction = {
  command: string;
  reason: string;
  risk: 'low' | 'medium' | 'high';
};

const riskColor: Record<RemediationAction['risk'], string> = { low: 'green', medium: 'orange', high: 'red' };

function parseActions(output: string): RemediationAction[] {
  try {
    const parsed = JSON.parse(output) as { actions?: RemediationAction[] };
    return Array.isArray(parsed.actions) ? parsed.actions : [];
  } catch {
    return [];
  }
}

function latestRemediation(runs: AgentRun[]): AgentRun | undefined {
  return runs.filter((run) => run.role === 'remediation').sort((a, b) => b.attempt - a.attempt)[0];
}

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

  const actions = useMemo(() => {
    const run = latestRemediation(runsQuery.data?.runs ?? []);
    return run ? parseActions(run.output) : [];
  }, [runsQuery.data]);

  const highRisk = actions.some((action) => action.risk === 'high');
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
                <div className="flex items-center justify-between gap-3">
                  <Typography.Text strong>方案 {index + 1}</Typography.Text>
                  <Tag color={riskColor[action.risk]}>风险 {action.risk}</Tag>
                </div>
                <Typography.Paragraph className="!mb-1 font-mono text-xs">{action.command}</Typography.Paragraph>
                <Typography.Paragraph type="secondary" className="!mb-0 text-sm">
                  {action.reason}
                </Typography.Paragraph>
              </div>
            ))}
          </div>

          {highRisk ? (
            <Alert type="error" showIcon message="高风险方案不允许直接批准，只能拒绝。" />
          ) : null}

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
            {!highRisk ? (
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
