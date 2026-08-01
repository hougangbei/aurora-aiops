import { beforeEach, describe, expect, it, vi } from 'vitest';

const getMock = vi.fn();
const postMock = vi.fn();

vi.mock('../../services/http', () => ({
  http: {
    get: (...args: unknown[]) => getMock(...args),
    post: (...args: unknown[]) => postMock(...args),
  },
}));

import {
  approveRemediation,
  createIncident,
  executeRemediation,
  getEvidence,
  getIncident,
  getRuns,
  listIncidents,
  reanalyzeIncident,
  rejectRemediation,
  rollbackIncident,
} from './api';

function okEnvelope<T>(data: T) {
  return { data: { code: 'OK', message: 'success', data } };
}

describe('aiops api client', () => {
  beforeEach(() => {
    getMock.mockReset();
    postMock.mockReset();
  });

  it('listIncidents GETs the list with optional filters', async () => {
    getMock.mockResolvedValue(okEnvelope([]));
    await listIncidents({ status: 'failed', namespace: 'default' });
    expect(getMock).toHaveBeenCalledWith('/aiops/incidents', {
      params: { status: 'failed', namespace: 'default' },
    });
  });

  it('getIncident GETs a single incident', async () => {
    getMock.mockResolvedValue(okEnvelope({ id: 'inc-1' }));
    await getIncident('inc/with?slash');
    expect(getMock).toHaveBeenCalledWith('/aiops/incidents/inc%2Fwith%3Fslash');
  });

  it('createIncident POSTs the create body', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'inc-2' }));
    await createIncident({
      summary: 'Pod crash',
      severity: 'critical',
      namespace: 'default',
      resourceKind: 'Pod',
      resourceName: 'api-0',
    });
    expect(postMock).toHaveBeenCalledWith('/aiops/incidents', {
      summary: 'Pod crash',
      severity: 'critical',
      namespace: 'default',
      resourceKind: 'Pod',
      resourceName: 'api-0',
    });
  });

  it('getEvidence GETs nodes and edges', async () => {
    getMock.mockResolvedValue(okEnvelope({ nodes: [], edges: [] }));
    await getEvidence('inc-1');
    expect(getMock).toHaveBeenCalledWith('/aiops/incidents/inc-1/evidence');
  });

  it('getRuns GETs agent runs', async () => {
    getMock.mockResolvedValue(okEnvelope({ runs: [] }));
    await getRuns('inc-1');
    expect(getMock).toHaveBeenCalledWith('/aiops/incidents/inc-1/runs');
  });

  it('reanalyzeIncident POSTs reanalyze', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'inc-1' }));
    await reanalyzeIncident('inc-1');
    expect(postMock).toHaveBeenCalledWith('/aiops/incidents/inc-1/reanalyze');
  });

  it('approveRemediation POSTs reason', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'inc-1' }));
    await approveRemediation('inc-1', 'verified safe');
    expect(postMock).toHaveBeenCalledWith('/aiops/incidents/inc-1/approve-remediation', {
      reason: 'verified safe',
    });
  });

  it('rejectRemediation POSTs reason', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'inc-1' }));
    await rejectRemediation('inc-1', 'too risky');
    expect(postMock).toHaveBeenCalledWith('/aiops/incidents/inc-1/reject-remediation', {
      reason: 'too risky',
    });
  });

  it('executeRemediation POSTs dryRun flag', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'inc-1' }));
    await executeRemediation('inc-1', true);
    expect(postMock).toHaveBeenCalledWith('/aiops/incidents/inc-1/execute-remediation', {
      dryRun: true,
    });
  });

  it('rollbackIncident POSTs rollback', async () => {
    postMock.mockResolvedValue(okEnvelope({ id: 'inc-1' }));
    await rollbackIncident('inc-1');
    expect(postMock).toHaveBeenCalledWith('/aiops/incidents/inc-1/rollback');
  });

  it('unwraps the envelope data', async () => {
    getMock.mockResolvedValue(okEnvelope({ id: 'inc-9' }));
    const incident = await getIncident('inc-9');
    expect(incident).toEqual({ id: 'inc-9' });
  });
});
