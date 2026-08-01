import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';

const MAX_RETRY_DELAY_MS = 15_000;
const INITIAL_RETRY_DELAY_MS = 1_000;

type UseIncidentEventsOptions = {
  incidentId: string;
  enabled: boolean;
};

// 建立到后端 SSE 事件流的同源 EventSource 连接（浏览器会自动携带 HttpOnly
// Session Cookie）。收到事件后刷新 Incident/证据/诊断记录查询缓存；断线指数
// 退避重连（上限 15 秒），重连时携带最后事件 id 以断点续传；组件卸载即关闭。
export function useIncidentEvents({ incidentId, enabled }: UseIncidentEventsOptions) {
  const queryClient = useQueryClient();
  const lastEventIdRef = useRef<number>(0);

  useEffect(() => {
    if (!enabled || !incidentId) return;

    let retryDelay = INITIAL_RETRY_DELAY_MS;
    let closed = false;
    let source: EventSource | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const refreshQueries = () => {
      void queryClient.invalidateQueries({ queryKey: ['aiops', 'incidents'] });
      void queryClient.invalidateQueries({ queryKey: ['aiops', 'incidents', incidentId] });
      void queryClient.invalidateQueries({ queryKey: ['aiops', 'runs', incidentId] });
      void queryClient.invalidateQueries({ queryKey: ['aiops', 'evidence', incidentId] });
    };

    const connect = () => {
      if (closed) return;
      const url = `/api/v1/aiops/incidents/${encodeURIComponent(incidentId)}/events?lastEventId=${lastEventIdRef.current}`;
      source = new EventSource(url);

      source.onopen = () => {
        retryDelay = INITIAL_RETRY_DELAY_MS;
      };

      source.onmessage = (event) => {
        const id = Number(event.lastEventId ?? 0);
        if (Number.isFinite(id) && id > lastEventIdRef.current) {
          lastEventIdRef.current = id;
        }
        refreshQueries();
      };

      source.onerror = () => {
        source?.close();
        if (closed) return;
        timer = setTimeout(connect, retryDelay);
        retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY_MS);
      };
    };

    connect();

    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      source?.close();
      source = null;
    };
  }, [enabled, incidentId, queryClient]);
}
