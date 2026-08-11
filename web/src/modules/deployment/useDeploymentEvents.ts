import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState } from 'react';

import { deploymentEventsUrl, getDeploymentTask } from './api';
import type { DeploymentEvent, DeploymentStep, DeploymentTask, DeploymentTaskDetail, TaskStatus } from './types';

const INITIAL_RETRY_DELAY_MS = 1000;
const MAX_RETRY_DELAY_MS = 15000;
const POLL_INTERVAL_MS = 2000;
const TERMINAL: TaskStatus[] = ['succeeded', 'failed', 'cancelled'];

type UseDeploymentEventsOptions = { taskId: string; enabled?: boolean };
type UseDeploymentEventsResult = {
  task?: DeploymentTask;
  steps: DeploymentStep[];
  events: DeploymentEvent[];
  connected: boolean;
  reconnecting: boolean;
  polling: boolean;
  failures: number;
  lastEventId: number;
  terminal: boolean;
};

function isTerminal(status?: TaskStatus): boolean {
  return Boolean(status && TERMINAL.includes(status));
}

function mergeDetail(previous: DeploymentTaskDetail | undefined, incoming: Partial<DeploymentTaskDetail>): DeploymentTaskDetail | undefined {
  if (!previous && !incoming.task) return undefined;
  const current = previous?.task;
  const next = incoming.task;
  const task = next
    ? {
        ...(current ?? next),
        ...next,
        percent: Math.max(current?.percent ?? 0, next.percent ?? 0),
      }
    : current;
  if (!task) return previous;
  return { task, steps: incoming.steps ?? previous?.steps ?? [] };
}

export function useDeploymentEvents({ taskId, enabled = true }: UseDeploymentEventsOptions): UseDeploymentEventsResult {
  const queryClient = useQueryClient();
  const queryKey = ['deployment-task', taskId];
  const query = useQuery({
    queryKey,
    queryFn: () => getDeploymentTask(taskId),
    enabled: enabled && Boolean(taskId),
  });
  const [events, setEvents] = useState<DeploymentEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [polling, setPolling] = useState(false);
  const [failures, setFailures] = useState(0);
  const lastEventIdRef = useRef(0);
  const finishedRef = useRef(false);
  const finalFetchRef = useRef(false);
  const failuresRef = useRef(0);
  const sourceRef = useRef<EventSource | null>(null);

  const setDetail = useCallback((incoming: DeploymentTaskDetail) => {
    queryClient.setQueryData<DeploymentTaskDetail | undefined>(queryKey, (previous) => mergeDetail(previous, incoming));
  }, [queryClient, queryKey]);

  const finalFetch = useCallback(() => {
    if (finalFetchRef.current || !taskId) return;
    finalFetchRef.current = true;
    void getDeploymentTask(taskId).then((detail) => setDetail(detail)).catch(() => undefined);
  }, [setDetail, taskId]);

  const finish = useCallback(() => {
    if (finishedRef.current) return;
    finishedRef.current = true;
    sourceRef.current?.close();
    sourceRef.current = null;
    setConnected(false);
    setReconnecting(false);
    setPolling(false);
    finalFetch();
  }, [finalFetch]);

  useEffect(() => {
    if (!enabled || !taskId) return;
    finishedRef.current = false;
    finalFetchRef.current = false;
    lastEventIdRef.current = 0;
    setEvents([]);
    setFailures(0);
    failuresRef.current = 0;
    setPolling(false);
    setConnected(false);
    let disposed = false;
    let source: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let pollTimer: ReturnType<typeof setTimeout> | undefined;
    let retryDelay = INITIAL_RETRY_DELAY_MS;

    const inspectTerminal = (detail: DeploymentTaskDetail | undefined) => {
      if (isTerminal(detail?.task.status)) finish();
    };

    const poll = async () => {
      if (disposed || finishedRef.current) return;
      try {
        const detail = await getDeploymentTask(taskId);
        if (disposed) return;
        setDetail(detail);
        inspectTerminal(detail);
      } catch {
        // Keep polling; the next scheduled request can recover a transient error.
      }
      if (!disposed && !finishedRef.current) pollTimer = setTimeout(poll, POLL_INTERVAL_MS);
    };

    const beginPolling = () => {
      if (disposed || finishedRef.current) return;
      setPolling(true);
      source?.close();
      source = null;
      if (!pollTimer) pollTimer = setTimeout(poll, POLL_INTERVAL_MS);
    };

    const connect = () => {
      if (disposed || finishedRef.current || polling) return;
      source = new EventSource(deploymentEventsUrl(taskId, lastEventIdRef.current));
      sourceRef.current = source;
      source.onopen = () => {
        if (disposed || finishedRef.current) return;
        setConnected(true);
        setReconnecting(false);
      };
      source.onmessage = (event) => {
        if (disposed || finishedRef.current) return;
        const eventId = Number(event.lastEventId);
        if (!Number.isSafeInteger(eventId) || eventId <= lastEventIdRef.current) return;
        lastEventIdRef.current = eventId;
        let payload: unknown;
        try { payload = JSON.parse(event.data); } catch { return; }
        const incoming = (payload && typeof payload === 'object' ? payload : {}) as Partial<DeploymentTaskDetail>;
        if (incoming.task) {
          setDetail(incoming as DeploymentTaskDetail);
          inspectTerminal(incoming as DeploymentTaskDetail);
        } else {
          // State transition events may contain only a type/payload; refresh the
          // durable task detail so the UI never relies on an event shape.
          void getDeploymentTask(taskId).then((detail) => {
            if (disposed || finishedRef.current) return;
            setDetail(detail);
            inspectTerminal(detail);
          }).catch(() => undefined);
        }
        setEvents((current) => [...current, { id: eventId, type: event.type, data: payload }].slice(-200));
      };
      source.onerror = () => {
        if (disposed || finishedRef.current) return;
        source?.close();
        source = null;
        setConnected(false);
        setReconnecting(true);
        const nextFailures = failuresRef.current + 1;
        failuresRef.current = nextFailures;
        setFailures(nextFailures);
        if (nextFailures >= 3) {
          beginPolling();
        } else {
          reconnectTimer = setTimeout(connect, retryDelay);
          retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY_MS);
        }
      };
    };

    connect();
    inspectTerminal(queryClient.getQueryData<DeploymentTaskDetail>(queryKey));
    return () => {
      disposed = true;
      source?.close();
      sourceRef.current = null;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (pollTimer) clearTimeout(pollTimer);
      source = null;
    };
    // The connection lifecycle should restart only when task identity or enabled changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, taskId]);

  useEffect(() => {
    if (isTerminal(query.data?.task.status)) finish();
  }, [finish, query.data?.task.status]);

  const detail = query.data;
  return {
    task: detail?.task,
    steps: detail?.steps ?? [],
    events,
    connected,
    reconnecting,
    polling,
    failures,
    lastEventId: lastEventIdRef.current,
    terminal: isTerminal(detail?.task.status),
  };
}
