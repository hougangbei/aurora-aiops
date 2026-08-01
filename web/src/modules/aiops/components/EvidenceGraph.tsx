import { Background, Controls, ReactFlow, type Node, type NodeProps, type Edge } from '@xyflow/react';
import '@xyflow/react/dist/base.css';
import { useState } from 'react';

import { evidenceKindColor, evidenceKindLabel, type EvidenceFlowNodeData, type EvidenceFlowResult } from '../evidenceLayout';
import type { EvidenceNode } from '../types';
import { EvidenceDrawer } from './EvidenceDrawer';

function EvidenceNodeCard({ data }: NodeProps<Node<EvidenceFlowNodeData>>) {
  const color = evidenceKindColor[data.kind] ?? '#94a3b8';
  return (
    <div
      className="w-[200px] rounded-lg border-2 bg-white px-3 py-2 shadow-sm"
      style={{ borderColor: color }}
    >
      <div className="text-xs font-bold uppercase" style={{ color }}>
        {evidenceKindLabel[data.kind] ?? data.kind}
      </div>
      <div className="truncate font-mono text-xs text-slate-500">{data.evidence.id}</div>
    </div>
  );
}

const nodeTypes = { evidence: EvidenceNodeCard };

export function EvidenceGraph({ flow }: { flow: EvidenceFlowResult }) {
  const [selected, setSelected] = useState<EvidenceNode | null>(null);

  return (
    <div className="h-[520px] rounded-xl border border-slate-200">
      <ReactFlow<Node<EvidenceFlowNodeData>, Edge>
        nodes={flow.nodes}
        edges={flow.edges}
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.2}
        maxZoom={1.6}
        nodesDraggable
        onNodeClick={(_, node) => setSelected(node.data.evidence)}
      >
        <Background color="#d8e0e7" gap={18} size={1} />
        <Controls />
      </ReactFlow>
      <EvidenceDrawer node={selected} onClose={() => setSelected(null)} />
    </div>
  );
}
