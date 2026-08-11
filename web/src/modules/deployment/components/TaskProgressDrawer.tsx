import { CloseOutlined, ReloadOutlined, StopOutlined } from '@ant-design/icons';
import { useMutation } from '@tanstack/react-query';
import { Alert, App, Button, Drawer, Empty, List, Progress, Space, Steps, Tag, Typography } from 'antd';

import { cancelDeploymentTask, retryDeploymentTask } from '../api';
import { useDeploymentEvents } from '../useDeploymentEvents';
import type { DeploymentEvent, StepStatus, TaskStatus } from '../types';

const statusLabel: Record<TaskStatus, string> = {
  queued: '排队中', running: '执行中', succeeded: '任务成功', failed: '任务失败', cancelled: '任务已取消',
};
const stepLabel: Record<StepStatus, string> = {
  pending: '待执行', running: '执行中', succeeded: '已完成', failed: '失败', skipped: '已跳过', cancelled: '已取消',
};

function redactLog(value: unknown): string {
  let output = typeof value === 'string' ? value : JSON.stringify(value ?? '') ?? '';
  output = output
    .replace(/-----BEGIN [^-]+-----[\s\S]*?-----END [^-]+-----/gi, '[REDACTED]')
    .replace(/(password|passphrase|token|secret|authorization|auth)\s*[:=]\s*([^\s,;]+)/gi, '$1=[REDACTED]')
    .replace(/bearer\s+[a-z0-9._~+/=-]+/gi, 'Bearer [REDACTED]');
  return output.length > 16 * 1024 ? `${output.slice(0, 16 * 1024)}…` : output;
}

function eventMessage(event: DeploymentEvent): string {
  if (event.data && typeof event.data === 'object' && 'message' in event.data) {
    return redactLog((event.data as { message?: unknown }).message);
  }
  return redactLog(event.data);
}

export type TaskProgressDrawerProps = {
  taskId?: string;
  open: boolean;
  canManage: boolean;
  onClose: () => void;
};

export function TaskProgressDrawer({ taskId, open, canManage, onClose }: TaskProgressDrawerProps) {
  const { message } = App.useApp();
  const stream = useDeploymentEvents({ taskId: taskId ?? '', enabled: open && Boolean(taskId) });
  const cancelMutation = useMutation({
    mutationFn: () => cancelDeploymentTask(taskId ?? ''),
    onSuccess: () => message.success('取消请求已提交'),
    onError: () => message.error('取消任务失败'),
  });
  const retryMutation = useMutation({
    mutationFn: () => retryDeploymentTask(taskId ?? ''),
    onSuccess: () => message.success('重试任务已提交'),
    onError: () => message.error('重试任务失败'),
  });
  const task = stream.task;
  const percent = Math.max(0, Math.min(100, task?.percent ?? 0));
  const terminal = stream.terminal;
  const steps = [...stream.steps].sort((a, b) => a.ordinal - b.ordinal);

  return (
    <Drawer open={open} onClose={onClose} width={520} title={task ? `任务进度 · ${task.projectId}` : '任务进度'}>
      {!task ? <Empty description="正在加载任务记录" /> : <div className="space-y-5">
        <Space className="w-full justify-between" align="center">
          <Tag color={task.status === 'failed' ? 'error' : task.status === 'succeeded' ? 'success' : 'processing'}>{statusLabel[task.status]}</Tag>
          <Typography.Text type="secondary">{task.currentStepLabel || '等待执行'}</Typography.Text>
        </Space>
        <Progress percent={percent} status={task.status === 'failed' ? 'exception' : task.status === 'succeeded' ? 'success' : undefined} />
        {stream.reconnecting ? <Alert type="warning" showIcon message={stream.polling ? '实时连接不可用，正在轮询恢复' : '实时连接已断开，正在重连'} /> : null}
        {terminal && task.status === 'succeeded' ? <Alert type="success" showIcon message="任务成功" /> : null}
        {terminal && task.status === 'failed' ? <Alert type="error" showIcon message="任务失败" description={task.errorMessage} /> : null}
        {terminal && task.status === 'cancelled' ? <Alert type="info" showIcon message="任务已取消" /> : null}
        <Steps direction="vertical" current={Math.max(0, steps.findIndex((step) => step.status === 'running'))} items={steps.map((step) => ({
          title: step.label,
          description: <Space size="small"><Tag>{stepLabel[step.status]}</Tag>{step.errorMessage ? <Typography.Text type="danger">{step.errorMessage}</Typography.Text> : null}</Space>,
          status: step.status === 'failed' ? 'error' : step.status === 'succeeded' || step.status === 'skipped' ? 'finish' : step.status === 'running' ? 'process' : 'wait',
        }))} />
        <div>
          <Typography.Text strong>执行日志</Typography.Text>
          <List size="small" bordered className="mt-2 max-h-64 overflow-y-auto" locale={{ emptyText: '暂无日志' }} dataSource={stream.events} renderItem={(event) => <List.Item><Typography.Text code className="whitespace-pre-wrap break-all">{eventMessage(event)}</Typography.Text></List.Item>} />
        </div>
        <Space>
          <Button aria-label="取消任务" icon={<StopOutlined />} danger disabled={!canManage || terminal || cancelMutation.isPending || task.cancelRequested} loading={cancelMutation.isPending} onClick={() => cancelMutation.mutate()}>{task.cancelRequested ? '取消请求已提交' : '取消任务'}</Button>
          <Button aria-label="重试任务" icon={<ReloadOutlined />} disabled={!canManage || !terminal || retryMutation.isPending} loading={retryMutation.isPending} onClick={() => retryMutation.mutate()}>重试任务</Button>
          <Button icon={<CloseOutlined />} onClick={onClose}>关闭</Button>
        </Space>
      </div>}
    </Drawer>
  );
}
