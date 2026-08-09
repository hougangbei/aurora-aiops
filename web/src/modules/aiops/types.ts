// 与 Go 后端 /api/v1/aiops 的 JSON 完全一致的领域类型。

export type IncidentStatus =
  | 'received'
  | 'triaging'
  | 'collecting'
  | 'analyzing'
  | 'proposing'
  | 'awaiting_approval'
  | 'approved'
  | 'executing'
  | 'resolved'
  | 'rejected'
  | 'failed';

export type IncidentSeverity = 'info' | 'warning' | 'critical';

export type Incident = {
  id: string;
  summary: string;
  severity: IncidentSeverity;
  status: IncidentStatus;
  namespace: string;
  resourceKind: string;
  resourceName: string;
  createdAt: string;
  updatedAt: string;
};

export type CreateIncidentInput = {
  summary: string;
  severity: IncidentSeverity;
  namespace: string;
  resourceKind: string;
  resourceName: string;
};

export type AIOpsReadiness = {
  modelConfigured: boolean;
  model: string;
  configurationSource: 'environment';
  runtimeMutable: boolean;
  deterministicRolesAvailable: boolean;
  remediationAvailable: boolean;
};

export type ClusterConnection = {
  state: 'connected' | 'degraded' | 'unreachable';
  currentContext?: string;
  serverHost?: string;
  version: string;
  latencyMs: number;
  checkedAt?: string;
  capabilities: {
    nodes: boolean;
    events: boolean;
    podLogs: boolean;
    metrics: boolean;
  };
};

export type EvidenceNodeKind = 'snapshot' | 'event' | 'log' | 'metric' | 'agent' | 'system';

export type EvidenceNode = {
  incidentId: string;
  id: string;
  kind: EvidenceNodeKind;
  payload: string;
  observedAt: string;
  hash: string;
  createdAt: string;
};

export type EvidenceRelation = 'supports' | 'contradicts';

export type EvidenceEdge = {
  incidentId: string;
  fromId: string;
  toId: string;
  relation: EvidenceRelation;
  createdAt: string;
};

export type AgentRunStatus = 'running' | 'succeeded' | 'failed' | 'skipped';

export type AgentRun = {
  id: string;
  incidentId: string;
  role: string;
  attempt: number;
  status: AgentRunStatus;
  summary: string;
  output: string;
  model: string;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  error: string;
  startedAt: string;
  completedAt: string;
};

// EffectiveRiskReview mirrors the Go aiops.EffectiveRiskReview JSON exactly. It
// is the only source of approvability surfaced to the approval UI; the nested
// model review is advisory context only.
export type PolicyActionReview = {
  kind: string;
  resourceKind: string;
  resourceName: string;
  allowed: boolean;
  risk: 'low' | 'medium' | 'high';
  reason: string;
};

export type ModelRiskReview = {
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  approved: boolean;
  blockers?: string[];
  rationale?: string;
};

export type EffectiveRiskReview = {
  effectiveRisk: 'low' | 'medium' | 'high';
  approvable: boolean;
  blockers?: string[];
  actions: PolicyActionReview[];
  modelReview: ModelRiskReview;
};

export type IncidentEventType = 'run_started' | 'run_completed' | 'incident_updated' | 'reanalyze_started';

export type IncidentEvent = {
  id: number;
  incidentId: string;
  type: IncidentEventType;
  data: string;
  createdAt: string;
};

export type ExperimentGroup = 'rules' | 'single_llm' | 'multi_agent';

export type ExperimentMetrics = {
  group: ExperimentGroup;
  sampleCount: number;
  top1Rate: number;
  top3Rate: number;
  avgMttdSeconds: number;
  evidenceCompletenessRate: number;
  highRiskInterceptionRate: number;
  avgTokens: number;
  confidenceInterval: number;
  insufficientSamples: boolean;
};
