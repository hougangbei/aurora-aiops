import { act, renderHook, waitFor } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createTestQueryClient } from '../../test/render';
import { getDeploymentTask } from './api';
import { useDeploymentEvents } from './useDeploymentEvents';

vi.mock('./api', () => ({
  getDeploymentTask: vi.fn(),
  deploymentEventsUrl: (id: string, lastEventId: number) => `/deployment-tasks/${id}/events?lastEventId=${lastEventId}`,
}));

type Source = {
  url: string;
  onopen: (() => void) | null;
  onmessage: ((event: { lastEventId: string; data: string }) => void) | null;
  onerror: (() => void) | null;
  close: () => void;
  closed: boolean;
};

const sources: Source[] = [];

class FakeEventSource implements Source {
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((event: { lastEventId: string; data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;

  constructor(url: string) {
    this.url = url;
    sources.push(this);
  }

  close() { this.closed = true; }
}

const task = { id: 'task-1', serverId: 'server-1', projectId: 'aurora', version: '1', actor: 'admin', action: 'install', status: 'running', percent: 20, cancelRequested: false, createdAt: '', updatedAt: '' };

describe('useDeploymentEvents', () => {
  beforeEach(() => {
    sources.length = 0;
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.mocked(getDeploymentTask).mockResolvedValue({ task, steps: [] } as never);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('resumes with the last event id, ignores duplicates, and never lowers percent', async () => {
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useDeploymentEvents({ taskId: 'task-1', enabled: true }), { wrapper });
    await waitFor(() => expect(sources).toHaveLength(1));
    expect(sources[0].url).toContain('/deployment-tasks/task-1/events?lastEventId=0');

    act(() => sources[0].onmessage?.({ lastEventId: '5', data: JSON.stringify({ task: { ...task, percent: 60 }, steps: [] }) }));
    act(() => sources[0].onmessage?.({ lastEventId: '4', data: JSON.stringify({ task: { ...task, percent: 10 }, steps: [] }) }));
    expect(result.current.task?.percent).toBe(60);

    vi.useFakeTimers();
    act(() => sources[0].onerror?.());
    act(() => vi.advanceTimersByTime(1000));
    expect(sources[1].url).toContain('lastEventId=5');
  });

  it('switches to polling after three failures and performs final fetch for terminal state', async () => {
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useDeploymentEvents({ taskId: 'task-1', enabled: true }), { wrapper });
    await waitFor(() => expect(sources).toHaveLength(1));
    vi.useFakeTimers();
    act(() => sources[0].onerror?.());
    act(() => vi.advanceTimersByTime(1000));
    act(() => sources[1].onerror?.());
    act(() => vi.advanceTimersByTime(2000));
    act(() => sources[2].onerror?.());
    expect(result.current.polling).toBe(true);
    vi.mocked(getDeploymentTask).mockResolvedValue({ task: { ...task, status: 'succeeded', percent: 100 }, steps: [] } as never);
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    vi.useRealTimers();
    await waitFor(() => expect(result.current.terminal).toBe(true));
    expect(getDeploymentTask).toHaveBeenCalled();
    expect(sources.every((source) => source.closed)).toBe(true);
  });
});
