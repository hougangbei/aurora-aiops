import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/base.css';
import { useState } from 'react';

import { evidenceKindColor, evidenceKindLabel, type EvidenceFlowNodeData, type EvidenceFlowResult } from '../evidenceLayout';
import type { EvidenceNode } from '../types';
import { EvidenceDrawer } from './EvidenceDrawer';

function EvidenceNodeCard({ data }: NodeProps<Node<EvidenceFlowNodeData>>) {
  const color = evidenceKindColor[data.kind] ?? '#94a3b8';
  return (
    <div
      className="relative w-[220px] rounded-xl border bg-[#151e33]/95 px-4 py-3 shadow-[0_12px_30px_rgba(2,6,23,0.38)]"
      style={{ borderColor: `${color}cc`, boxShadow: `0 0 0 1px ${color}22, 0 12px 30px rgba(2,6,23,.38)` }}
    >
      <Handle type="target" position={Position.Top} className="!h-2.5 !w-2.5 !border-2 !border-[#0a0f1c]" style={{ background: color }} />
      <div className="text-xs font-bold uppercase" style={{ color }}>
        {evidenceKindLabel[data.kind] ?? data.kind}
      </div>
      <div className="mt-1 truncate font-mono text-xs text-slate-300">{data.evidence.id}</div>
      <Handle type="source" position={Position.Bottom} className="!h-2.5 !w-2.5 !border-2 !border-[#0a0f1c]" style={{ background: color }} />
    </div>
  );
}

const nodeTypes = { evidence: EvidenceNodeCard };

export function EvidenceGraph({ flow }: { flow: EvidenceFlowResult }) {
  const [selected, setSelected] = useState<EvidenceNode | null>(null);

  return (
    <div className="h-[520px] overflow-hidden rounded-xl border border-indigo-200/20 bg-[#0b1120]">
      <ReactFlow<Node<EvidenceFlowNodeData>, Edge>
        nodes={flow.nodes}
        edges={flow.edges}
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.2}
        maxZoom={1.6}
        nodesDraggable
        defaultEdgeOptions={{
          type: 'smoothstep',
          style: { stroke: '#9aaeff', strokeWidth: 2 },
          labelStyle: { fill: '#e7ecff', fontSize: 11, fontWeight: 600 },
          labelBgStyle: { fill: '#141c2f', fillOpacity: 0.94 },
          labelBgPadding: [6, 4],
          labelBgBorderRadius: 6,
          markerEnd: { type: MarkerType.ArrowClosed, color: '#9aaeff' },
        }}
        onNodeClick={(_, node) => setSelected(node.data.evidence)}
      >
        <Background color="#334368" gap={18} size={1} />
        <Controls />
      </ReactFlow>
      <EvidenceDrawer node={selected} onClose={() => setSelected(null)} />
    </div>
  );
}
