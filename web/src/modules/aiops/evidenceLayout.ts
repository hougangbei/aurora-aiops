import type { Edge as FlowEdge, Node as FlowNode } from '@xyflow/react';
import ELK from 'elkjs/lib/elk.bundled.js';

import type { EvidenceEdge, EvidenceNode } from './types';

// 超过该节点数时按类型折叠，避免页面冻结。
export const COLLAPSE_THRESHOLD = 200;

// 折叠时保留的类型（快照 / 事件 / Agent 逐条展示）。
const keepKinds = new Set(['snapshot', 'event', 'agent']);

export const evidenceKindColor: Record<string, string> = {
  snapshot: '#0f766e',
  event: '#2563eb',
  log: '#7c3aed',
  metric: '#d97706',
  agent: '#059669',
  system: '#64748b',
};

export const evidenceKindLabel: Record<string, string> = {
  snapshot: '快照',
  event: '事件',
  log: '日志',
  metric: '指标',
  agent: 'Agent',
  system: '系统',
};

export type EvidenceFlowNodeData = {
  evidence: EvidenceNode;
  kind: string;
};

export type EvidenceFlowResult = {
  nodes: FlowNode<EvidenceFlowNodeData>[];
  edges: FlowEdge[];
  truncated: boolean;
  collapsed: Record<string, number>;
};

const elk = new ELK();

// buildEvidenceLayout 是纯布局函数：相同输入总是产生相同位置；空图返回空结果
// 而不抛错。使用项目既有的 ELK（elkjs）分层布局。
export async function buildEvidenceLayout(
  nodes: EvidenceNode[],
  edges: EvidenceEdge[],
  options: { forceShowAll?: boolean } = {},
): Promise<EvidenceFlowResult> {
  const { flowNodes, flowEdges, collapsed, truncated } = collapseIfNeeded(nodes, edges, options.forceShowAll ?? false);

  if (flowNodes.length === 0) {
    return { nodes: [], edges: [], truncated: false, collapsed: {} };
  }

  const graph = await elk.layout({
    id: 'evidence-root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': 'DOWN',
      'elk.spacing.nodeNode': '48',
      'elk.spacing.componentComponent': '48',
    },
    children: flowNodes.map((node) => ({ id: node.id, width: 220, height: 64 })),
    edges: flowEdges.map((edge) => ({
      id: `${edge.fromId}-${edge.toId}`,
      sources: [edge.fromId],
      targets: [edge.toId],
    })),
  });

  const positioned = new Map<string, { x: number; y: number }>();
  for (const child of graph.children ?? []) {
    positioned.set(child.id, { x: child.x ?? 0, y: child.y ?? 0 });
  }

  const nodesOut: FlowNode<EvidenceFlowNodeData>[] = flowNodes.map((node) => {
    const pos = positioned.get(node.id) ?? { x: 0, y: 0 };
    return { id: node.id, position: pos, data: { evidence: node, kind: node.kind }, type: 'evidence' };
  });

  const edgesOut: FlowEdge[] = flowEdges.map((edge) => ({
    id: `${edge.fromId}-${edge.toId}`,
    source: edge.fromId,
    target: edge.toId,
    label: edge.relation,
  }));

  return { nodes: nodesOut, edges: edgesOut, truncated, collapsed };
}

type CollapseResult = {
  flowNodes: EvidenceNode[];
  flowEdges: EvidenceEdge[];
  collapsed: Record<string, number>;
  truncated: boolean;
};

function collapseIfNeeded(nodes: EvidenceNode[], edges: EvidenceEdge[], forceShowAll: boolean): CollapseResult {
  if (forceShowAll || nodes.length <= COLLAPSE_THRESHOLD) {
    return { flowNodes: nodes, flowEdges: edges, collapsed: {}, truncated: false };
  }

  const collapsed: Record<string, number> = {};
  const flowNodes: EvidenceNode[] = [];
  const idMap = new Map<string, string>();

  for (const node of nodes) {
    if (keepKinds.has(node.kind)) {
      flowNodes.push(node);
      idMap.set(node.id, node.id);
    } else {
      collapsed[node.kind] = (collapsed[node.kind] ?? 0) + 1;
      idMap.set(node.id, `collapsed-${node.kind}`);
    }
  }

  for (const [kind, count] of Object.entries(collapsed)) {
    const now = new Date(0).toISOString();
    flowNodes.push({
      id: `collapsed-${kind}`,
      incidentId: nodes[0]?.incidentId ?? '',
      kind: kind as EvidenceNode['kind'],
      payload: JSON.stringify({ collapsed: true, kind, count }),
      observedAt: now,
      hash: '',
      createdAt: now,
    });
  }

  const flowEdges = edges
    .map((edge) => ({
      ...edge,
      fromId: idMap.get(edge.fromId) ?? edge.fromId,
      toId: idMap.get(edge.toId) ?? edge.toId,
    }))
    .filter((edge) => edge.fromId !== edge.toId);

  return { flowNodes, flowEdges, collapsed, truncated: true };
}
