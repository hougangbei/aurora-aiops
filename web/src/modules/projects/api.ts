import { http } from '../../services/http';
import type { InstallProjectInput, InstallProjectResult, Project } from './types';

type Envelope<T> = { code: string; message?: string; data: T };

function unwrap<T>(response: { data: Envelope<T> }): T {
  return response.data.data;
}

const projectPath = (id: string) => `/projects/${encodeURIComponent(id)}`;

export async function listProjects(): Promise<Project[]> {
  return unwrap(await http.get('/projects'));
}

export async function getProject(id: string): Promise<Project> {
  return unwrap(await http.get(projectPath(id)));
}

export async function installProject(id: string, input: InstallProjectInput): Promise<InstallProjectResult> {
  return unwrap(await http.post(`${projectPath(id)}/install`, input));
}
