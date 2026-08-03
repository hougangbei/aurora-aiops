import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../../test/render';
import { useAppStore } from '../../../stores/appStore';
import { ApprovalDialog } from './ApprovalDialog';

const getRunsMock = vi.fn();
const approveMock = vi.fn();
const rejectMock = vi.fn();

vi.mock('../api', () => ({
  getRuns: (...args: unknown[]) => getRunsMock(...args),
  approveRemediation: (...args: unknown[]) => approveMock(...args),
  rejectRemediation: (...args: unknown[]) => rejectMock(...args),
}));

type ReviewFixture = {
  effectiveRisk: 'low' | 'medium' | 'high';
  approvable: boolean;
  blockers?: string[];
  actions: Array<{
    kind: string;
    resourceKind: string;
    resourceName: string;
    allowed: boolean;
    risk: 'low' | 'medium' | 'high';
    reason: string;
  }>;
  modelReview: {
    riskLevel: 'low' | 'medium' | 'high' | 'critical';
    approved: boolean;
    blockers?: string[];
    rationale?: string;
  };
};

function run(id: string, role: string, output: unknown) {
  return {
    id,
    incidentId: 'inc-1',
    role,
    attempt: 1,
    status: 'succeeded',
    summary: '',
    output: JSON.stringify(output),
    model: '',
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    error: '',
    startedAt: '',
    completedAt: '',
  };
}

function remediationOnlyRun(risk: 'low' | 'medium' | 'high') {
  return run('run-rem', 'remediation', {
    actions: [{ command: `kubectl action (${risk})`, reason: 'restart after crash', risk }],
  });
}

function reviewFixture(review: ReviewFixture, remediationRisk: 'low' | 'medium' | 'high' = 'medium') {
  return { runs: [remediationOnlyRun(remediationRisk), run('run-risk', 'risk_review', review)] };
}

function reasonTextarea() {
  return screen.getByPlaceholderText(/说明批准或拒绝的理由/i) as HTMLTextAreaElement;
}

function renderDialog() {
  return renderWithProviders(<ApprovalDialog incidentId="inc-1" open onClose={() => {}} />);
}

describe('ApprovalDialog', () => {
  beforeEach(() => {
    useAppStore.setState({ authenticated: true, dataMode: 'live' });
    getRunsMock.mockReset();
    approveMock.mockReset();
    rejectMock.mockReset();
  });

  it('hides approve when the effective review is high/not approvable (even if remediation claims low)', async () => {
    getRunsMock.mockResolvedValue(
      reviewFixture(
        {
          effectiveRisk: 'high',
          approvable: false,
          blockers: ['policy: action namespace must match the incident namespace'],
          actions: [
            {
              kind: 'restart_deployment',
              resourceKind: 'Deployment',
              resourceName: 'api-0',
              allowed: false,
              risk: 'high',
              reason: 'namespace mismatch',
            },
          ],
          modelReview: { riskLevel: 'low', approved: true },
        },
        'low',
      ),
    );
    renderDialog();
    await screen.findByRole('button', { name: /拒\s*绝/i });
    expect(screen.queryByRole('button', { name: /批准执行/i })).toBeNull();
  });

  it('shows approve enabled after 8 chars when the effective review is approvable (even if remediation claims high)', async () => {
    getRunsMock.mockResolvedValue(
      reviewFixture(
        {
          effectiveRisk: 'medium',
          approvable: true,
          actions: [
            {
              kind: 'restart_deployment',
              resourceKind: 'Deployment',
              resourceName: 'api-0',
              allowed: true,
              risk: 'medium',
              reason: 'restart deployment',
            },
          ],
          modelReview: { riskLevel: 'high', approved: false },
        },
        'high',
      ),
    );
    renderDialog();
    const approve = await screen.findByRole('button', { name: /批准执行/i });
    expect(approve).toBeDisabled();

    const user = userEvent.setup();
    await user.type(reasonTextarea(), '太短');
    expect(screen.getByRole('button', { name: /批准执行/i })).toBeDisabled();

    await user.type(reasonTextarea(), '这是超过八个字的详细审批理由');
    expect(screen.getByRole('button', { name: /批准执行/i })).toBeEnabled();
  });

  it('fail-closes with an error and no approve button when no succeeded risk_review run exists', async () => {
    getRunsMock.mockResolvedValue({ runs: [remediationOnlyRun('medium')] });
    renderDialog();
    await screen.findByText(/有效风险评审缺失/i);
    expect(screen.queryByRole('button', { name: /批准执行/i })).toBeNull();
  });

  it('fail-closes when the effective risk enum is invalid', async () => {
    getRunsMock.mockResolvedValue({
      runs: [
        remediationOnlyRun('low'),
        run('run-risk', 'risk_review', {
          effectiveRisk: 'critical',
          approvable: true,
          actions: [
            {
              kind: 'restart_deployment',
              resourceKind: 'Deployment',
              resourceName: 'api-0',
              allowed: true,
              risk: 'low',
              reason: 'restart deployment',
            },
          ],
          modelReview: { riskLevel: 'low', approved: true },
        }),
      ],
    });
    renderDialog();
    await screen.findByText(/有效风险评审缺失/i);
    expect(screen.queryByRole('button', { name: /批准执行/i })).toBeNull();
  });

  it('fail-closes without crashing when blockers has the wrong shape', async () => {
    getRunsMock.mockResolvedValue({
      runs: [
        remediationOnlyRun('low'),
        run('run-risk', 'risk_review', {
          effectiveRisk: 'low',
          approvable: true,
          blockers: 'not-an-array',
          actions: [
            {
              kind: 'suspend_cronjob',
              resourceKind: 'CronJob',
              resourceName: 'job-0',
              allowed: true,
              risk: 'low',
              reason: 'suspend cronjob',
            },
          ],
          modelReview: { riskLevel: 'low', approved: true },
        }),
      ],
    });
    renderDialog();
    await screen.findByText(/有效风险评审缺失/i);
    expect(screen.queryByRole('button', { name: /批准执行/i })).toBeNull();
  });

  it('disables repeated approval clicks while pending', async () => {
    getRunsMock.mockResolvedValue(
      reviewFixture({
        effectiveRisk: 'low',
        approvable: true,
        actions: [
          {
            kind: 'suspend_cronjob',
            resourceKind: 'CronJob',
            resourceName: 'job-0',
            allowed: true,
            risk: 'low',
            reason: 'suspend cronjob',
          },
        ],
        modelReview: { riskLevel: 'low', approved: true },
      }),
    );
    approveMock.mockReturnValue(new Promise(() => {}));
    renderDialog();
    const user = userEvent.setup();
    const textarea = await screen.findByPlaceholderText(/说明批准或拒绝的理由/i);
    await user.type(textarea, '这是超过八个字的详细审批理由');
    await user.click(screen.getByRole('button', { name: /批准执行/i }));
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /批准执行/i })).toBeDisabled();
    });
  });
});
