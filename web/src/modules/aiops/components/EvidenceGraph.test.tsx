import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { EvidenceFlowResult } from '../evidenceLayout';
import { EvidenceGraph } from './EvidenceGraph';

const flow: EvidenceFlowResult = {
  nodes: [
    {
      id: 'snapshot',
      position: { x: 0, y: 0 },
      type: 'evidence',
      data: {
        kind: 'snapshot',
        evidence: {
          incidentId: 'inc-1',
          id: 'snapshot',
          kind: 'snapshot',
          payload: '{}',
          observedAt: '2026-08-09T04:00:00.000Z',
          hash: 'abc',
          createdAt: '2026-08-09T04:00:00.000Z',
        },
      },
    },
    {
      id: 'event',
      position: { x: 0, y: 120 },
      type: 'evidence',
      data: {
        kind: 'event',
        evidence: {
          incidentId: 'inc-1',
          id: 'event',
          kind: 'event',
          payload: '{}',
          observedAt: '2026-08-09T04:00:00.000Z',
          hash: 'def',
          createdAt: '2026-08-09T04:00:00.000Z',
        },
      },
    },
  ],
  edges: [{ id: 'edge-1', source: 'snapshot', target: 'event', label: 'supports' }],
  truncated: false,
  collapsed: {},
};

describe('EvidenceGraph', () => {
  it('renders connection handles so evidence relations are visible', () => {
    const { container } = render(<EvidenceGraph flow={flow} />);
    expect(container.querySelectorAll('.react-flow__handle')).toHaveLength(4);
  });
});
