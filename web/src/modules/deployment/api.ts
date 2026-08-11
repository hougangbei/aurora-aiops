import { http } from '../../services/http';
import type { DeploymentTask, DeploymentTaskDetail } from './types';

type Envelope<T> = { code: string; message?: string; data: T };

function unwrap<T>(response: { data: Envelope<T> }): T {
  return response.data.data;
}
const taskPath = (id: string) => `/deployment-tasks/${encodeURIComponent(id)}`;
const serverPath = (id: string) => `/assets/servers/${encodeURIComponent(id)}`;

export async function listDeploymentTasks(serverId: string): Promise<DeploymentTask[]> {
  return unwrap(await http.get(`${serverPath(serverId)}/tasks`));
}

export async function listDeploymentInstallations(serverId: string): Promise<DeploymentTask[]> {
  return unwrap(await http.get(`${serverPath(serverId)}/installations`));
}

export async function getDeploymentTask(id: string): Promise<DeploymentTaskDetail> {
  return unwrap(await http.get(taskPath(id)));
}

export async function cancelDeploymentTask(id: string): Promise<DeploymentTask> {
  return unwrap(await http.post(`${taskPath(id)}/cancel`));
}

export async function retryDeploymentTask(id: string): Promise<DeploymentTask> {
  return unwrap(await http.post(`${taskPath(id)}/retry`));
}

export function deploymentEventsUrl(id: string, lastEventId: number): string {
  return `${taskPath(id)}/events?lastEventId=${Math.max(0, Math.floor(lastEventId))}`;
}
