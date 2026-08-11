export type TaskStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
export type TaskAction = 'install' | 'adopt';
export type StepStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'skipped' | 'cancelled';

export type DeploymentTask = {
  id: string;
  serverId: string;
  projectId: string;
  version: string;
  actor: string;
  retryOf?: string;
  action: TaskAction;
  status: TaskStatus;
  currentStepId?: string;
  currentStepLabel?: string;
  errorCode?: string;
  errorMessage?: string;
  percent: number;
  cancelRequested: boolean;
  createdAt: string;
  updatedAt: string;
  startedAt?: string;
  finishedAt?: string;
};

export type DeploymentStep = {
  taskId: string;
  id: string;
  label: string;
  ordinal: number;
  percent: number;
  status: StepStatus;
  startedAt?: string;
  finishedAt?: string;
  errorMessage?: string;
};

export type DeploymentTaskDetail = { task: DeploymentTask; steps: DeploymentStep[] };

export type DeploymentEvent = {
  id: number;
  type?: string;
  data: unknown;
};

