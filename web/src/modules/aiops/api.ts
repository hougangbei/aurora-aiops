import { http } from '../../services/http';
import type {
  AgentRun,
  AIOpsReadiness,
  ClusterConnection,
  CreateIncidentInput,
  EvidenceEdge,
  EvidenceNode,
  ExperimentMetrics,
  Incident,
} from './types';

// 所有函数复用 services/http（withCredentials + Session Cookie），
// 不在浏览器保存 Kubernetes Token 或 Session 原文。

// unwrap 解析统一信封 {code, message, data}，只返回 data。
function unwrap<T>(response: { data: { code: string; message?: string; data: T } }): T {
  return response.data.data;
}

export async function listIncidents(params?: { status?: string; namespace?: string }): Promise<Incident[]> {
  const response = await http.get('/aiops/incidents', { params });
  return unwrap(response);
}

export async function getAIOpsReadiness(): Promise<AIOpsReadiness> {
  const response = await http.get('/aiops/readiness');
  return unwrap(response);
}

export async function getClusterConnection(): Promise<ClusterConnection> {
  const response = await http.get('/cluster/connection');
  return unwrap(response);
}

export async function getIncident(id: string): Promise<Incident> {
  const response = await http.get(`/aiops/incidents/${encodeURIComponent(id)}`);
  return unwrap(response);
}

export async function createIncident(input: CreateIncidentInput): Promise<Incident> {
  const response = await http.post('/aiops/incidents', input);
  return unwrap(response);
}

export async function getEvidence(id: string): Promise<{ nodes: EvidenceNode[]; edges: EvidenceEdge[] }> {
  const response = await http.get(`/aiops/incidents/${encodeURIComponent(id)}/evidence`);
  return unwrap(response);
}

export async function getRuns(id: string): Promise<{ runs: AgentRun[] }> {
  const response = await http.get(`/aiops/incidents/${encodeURIComponent(id)}/runs`);
  return unwrap(response);
}

export async function reanalyzeIncident(id: string): Promise<Incident> {
  const response = await http.post(`/aiops/incidents/${encodeURIComponent(id)}/reanalyze`);
  return unwrap(response);
}

// 以下为计划 04 的审批 / 执行端点契约；当前后端未实现，前端先行定义以便
// 详情页在 04 落地后直接可用。
export async function approveRemediation(id: string, reason: string): Promise<Incident> {
  const response = await http.post(`/aiops/incidents/${encodeURIComponent(id)}/approve-remediation`, { reason });
  return unwrap(response);
}

export async function rejectRemediation(id: string, reason: string): Promise<Incident> {
  const response = await http.post(`/aiops/incidents/${encodeURIComponent(id)}/reject-remediation`, { reason });
  return unwrap(response);
}

export async function executeRemediation(id: string, dryRun = true): Promise<Incident> {
  const response = await http.post(`/aiops/incidents/${encodeURIComponent(id)}/execute-remediation`, { dryRun });
  return unwrap(response);
}

export async function rollbackIncident(id: string): Promise<Incident> {
  const response = await http.post(`/aiops/incidents/${encodeURIComponent(id)}/rollback`);
  return unwrap(response);
}

export async function getExperimentMetrics(): Promise<ExperimentMetrics[]> {
  const response = await http.get<{ code: string; data: { metrics: ExperimentMetrics[] } }>('/experiments/metrics');
  return response.data.data.metrics;
}
