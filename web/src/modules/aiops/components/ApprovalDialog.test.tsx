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

function remediationRun(actions: Array<{ command: string; reason: string; risk: string }>) {
  // getRuns 在 api.ts 中已解包统一信封，返回 { runs: [...] }。
  return {
    runs: [
      {
        id: 'run-1',
        incidentId: 'inc-1',
        role: 'remediation',
        attempt: 1,
        status: 'succeeded',
        summary: '',
        output: JSON.stringify({ actions }),
        model: '',
        promptTokens: 0,
        completionTokens: 0,
        totalTokens: 0,
        error: '',
        startedAt: '',
        completedAt: '',
      },
    ],
  };
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

  it('hides the approve button for a high-risk plan and only allows rejection', async () => {
    getRunsMock.mockResolvedValue(
      remediationRun([{ command: 'kubectl delete namespace prod', reason: 'dangerous', risk: 'high' }]),
    );
    renderDialog();
    await screen.findByText('高风险方案不允许直接批准，只能拒绝。');
    expect(screen.queryByRole('button', { name: /批准执行/i })).toBeNull();
    // antd 对两个汉字按钮文本插入空格，用容忍空白的正则匹配。
    expect(screen.getByRole('button', { name: /拒\s*绝/i })).toBeTruthy();
  });

  it('requires at least 8 chars of reason for a medium-risk approval', async () => {
    getRunsMock.mockResolvedValue(
      remediationRun([{ command: 'kubectl rollout restart deploy/api', reason: 'restart', risk: 'medium' }]),
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

  it('disables repeated approval clicks while pending', async () => {
    getRunsMock.mockResolvedValue(
      remediationRun([{ command: 'kubectl get pods', reason: 'safe', risk: 'low' }]),
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
