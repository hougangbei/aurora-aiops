import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from '../../../test/render';
import type { AgentRun } from '../types';
import { AgentTimeline } from './AgentTimeline';

function makeRun(overrides: Partial<AgentRun>): AgentRun {
  return {
    id: 'run-1',
    incidentId: 'inc-1',
    role: 'triage',
    attempt: 1,
    status: 'succeeded',
    summary: '',
    output: '',
    model: '',
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    error: '',
    startedAt: '',
    completedAt: '',
    ...overrides,
  };
}

describe('AgentTimeline', () => {
  it('renders the five roles in fixed order', () => {
    renderWithProviders(<AgentTimeline runs={[]} />);
    const items = screen.getAllByRole('listitem');
    expect(items.length).toBe(5);
    const labels = items.map((item) => item.querySelector('strong')?.textContent);
    expect(labels).toEqual(['分类', '证据采集', '根因分析', '修复建议', '风险评估']);
  });

  it('shows a waiting state for roles that have not started', () => {
    renderWithProviders(<AgentTimeline runs={[]} />);
    expect(screen.getAllByText('等待').length).toBe(5);
    expect(screen.getAllByText('尚未开始').length).toBe(5);
  });

  it('shows an in-progress state for a running role', () => {
    renderWithProviders(
      <AgentTimeline
        runs={[makeRun({ role: 'collector', status: 'running' })]}
      />,
    );
    expect(screen.getByText('进行中')).toBeTruthy();
    expect(screen.getByText('诊断进行中...')).toBeTruthy();
  });

  it('shows the error summary for a failed role', () => {
    renderWithProviders(
      <AgentTimeline
        runs={[makeRun({ role: 'root_cause', status: 'failed', error: '模型返回非法 JSON' })]}
      />,
    );
    expect(screen.getByText('失败')).toBeTruthy();
    expect(screen.getByText('模型返回非法 JSON')).toBeTruthy();
  });

  it('never renders the raw model output (hidden reasoning)', () => {
    const hiddenReasoning = '我先逐步推理，然后再输出结论';
    renderWithProviders(
      <AgentTimeline
        runs={[makeRun({ role: 'triage', status: 'succeeded', summary: 'Pod 崩溃', output: hiddenReasoning })]}
      />,
    );
    expect(screen.getByText('Pod 崩溃')).toBeTruthy();
    expect(screen.queryByText(hiddenReasoning)).toBeNull();
  });

  it('picks the latest attempt for a repeated role', () => {
    renderWithProviders(
      <AgentTimeline
        runs={[
          makeRun({ id: 'run-1', attempt: 1, role: 'triage', status: 'failed', error: 'old error' }),
          makeRun({ id: 'run-2', attempt: 2, role: 'triage', status: 'succeeded', summary: 'new summary' }),
        ]}
      />,
    );
    expect(screen.getByText('new summary')).toBeTruthy();
    expect(screen.queryByText('old error')).toBeNull();
  });
});
