import { Descriptions, Drawer } from 'antd';

import { formatTime } from '../format';
import { evidenceKindLabel } from '../evidenceLayout';
import type { EvidenceNode } from '../types';

export function EvidenceDrawer({ node, onClose }: { node: EvidenceNode | null; onClose: () => void }) {
  return (
    <Drawer title="证据详情" open={Boolean(node)} onClose={onClose} width={440} destroyOnClose>
      {node ? (
        <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label="ID">
            <span className="font-mono text-xs">{node.id}</span>
          </Descriptions.Item>
          <Descriptions.Item label="类型">{evidenceKindLabel[node.kind] ?? node.kind}</Descriptions.Item>
          <Descriptions.Item label="观测时间">{formatTime(node.observedAt)}</Descriptions.Item>
          <Descriptions.Item label="SHA-256">
            <span className="font-mono text-xs">{node.hash || '--'}</span>
          </Descriptions.Item>
          <Descriptions.Item label="脱敏载荷">
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-md border border-indigo-200/10 bg-[#0b1120] p-3 text-xs text-slate-200">
              {node.payload}
            </pre>
          </Descriptions.Item>
        </Descriptions>
      ) : null}
    </Drawer>
  );
}
