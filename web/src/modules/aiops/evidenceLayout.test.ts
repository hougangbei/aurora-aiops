import { describe, expect, it } from 'vitest';

import { buildEvidenceLayout } from './evidenceLayout';
import type { EvidenceEdge, EvidenceNode } from './types';

const t0 = '2026-08-01T08:00:00.000Z';

const nodes: EvidenceNode[] = [
  { incidentId: 'inc-1', id: 'snapshot', kind: 'snapshot', payload: '{"phase":"Running"}', observedAt: t0, hash: 'a'.repeat(64), createdAt: t0 },
  { incidentId: 'inc-1', id: 'log-app', kind: 'log', payload: 'level=error', observedAt: t0, hash: 'b'.repeat(64), createdAt: t0 },
  { incidentId: 'inc-1', id: 'event-1', kind: 'event', payload: '{}', observedAt: t0, hash: 'c'.repeat(64), createdAt: t0 },
];

const edges: EvidenceEdge[] = [{ incidentId: 'inc-1', fromId: 'log-app', toId: 'snapshot', relation: 'supports', createdAt: t0 }];

describe('buildEvidenceLayout', () => {
  it('is deterministic for the same input', async () => {
    const first = await buildEvidenceLayout(nodes, edges);
    const second = await buildEvidenceLayout(nodes, edges);
    const snapshot = (result: typeof first) =>
      result.nodes.map((n) => [n.id, n.position.x, n.position.y]).sort((a, b) => String(a[0]).localeCompare(String(b[0])));
    expect(snapshot(first)).toEqual(snapshot(second));
  });

  it('does not throw for an empty graph and returns empty result', async () => {
    const result = await buildEvidenceLayout([], []);
    expect(result.nodes).toEqual([]);
    expect(result.edges).toEqual([]);
    expect(result.truncated).toBe(false);
  });

  it('collapses over-threshold nodes by kind and marks truncated', async () => {
    const manyLogs: EvidenceNode[] = Array.from({ length: 250 }, (_, i) => ({
      incidentId: 'inc-1',
      id: `log-${i}`,
      kind: 'log',
      payload: `line ${i}`,
      observedAt: t0,
      hash: String(i),
      createdAt: t0,
    }));
    const result = await buildEvidenceLayout(manyLogs, []);
    expect(result.truncated).toBe(true);
    expect(result.collapsed.log).toBe(250);
    expect(result.nodes.some((n) => n.id === 'collapsed-log')).toBe(true);
  });

  it('keeps individual snapshot/event nodes when collapsing', async () => {
    const manyLogs: EvidenceNode[] = Array.from({ length: 250 }, (_, i) => ({
      incidentId: 'inc-1',
      id: `log-${i}`,
      kind: 'log',
      payload: `line ${i}`,
      observedAt: t0,
      hash: String(i),
      createdAt: t0,
    }));
    const all = [nodes[0], nodes[2], ...manyLogs];
    const result = await buildEvidenceLayout(all, []);
    expect(result.nodes.some((n) => n.id === 'snapshot')).toBe(true);
    expect(result.nodes.some((n) => n.id === 'event-1')).toBe(true);
  });
});
