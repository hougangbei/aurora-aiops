import { useQuery } from '@tanstack/react-query';
import { waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../test/render';
import { useIncidentEvents } from './useIncidentEvents';

type FakeMessage = { lastEventId?: string; data?: string };

const instances: FakeEventSource[] = [];

class FakeEventSource {
  onopen: (() => void) | null = null;
  onmessage: ((event: FakeMessage) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  url: string;

  constructor(url: string) {
    this.url = url;
    instances.push(this);
  }

  close() {
    this.closed = true;
  }
}

let fetchCount = 0;

function Probe({ incidentId, enabled }: { incidentId: string; enabled: boolean }) {
  useIncidentEvents({ incidentId, enabled });
  const query = useQuery({
    queryKey: ['aiops', 'incidents', incidentId],
    queryFn: async () => {
      fetchCount += 1;
      return fetchCount;
    },
  });
  return <div data-testid="value">{String(query.data ?? '')}</div>;
}

describe('useIncidentEvents', () => {
  beforeEach(() => {
    instances.length = 0;
    fetchCount = 0;
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('opens a same-origin EventSource with lastEventId=0', () => {
    renderWithProviders(<Probe incidentId="inc-1" enabled />);
    expect(instances).toHaveLength(1);
    expect(instances[0].url).toBe('/api/v1/aiops/incidents/inc-1/events?lastEventId=0');
  });

  it('does not connect when disabled', () => {
    renderWithProviders(<Probe incidentId="inc-1" enabled={false} />);
    expect(instances).toHaveLength(0);
  });

  it('refreshes the query cache when an SSE event arrives', async () => {
    renderWithProviders(<Probe incidentId="inc-1" enabled />);
    await waitFor(() => expect(fetchCount).toBe(1));

    instances[0].onmessage?.({ lastEventId: '3', data: '{}' });

    await waitFor(() => expect(fetchCount).toBeGreaterThanOrEqual(2));
  });

  it('closes the connection when the component unmounts', () => {
    const { unmount } = renderWithProviders(<Probe incidentId="inc-1" enabled />);
    const source = instances[0];
    expect(source.closed).toBe(false);
    unmount();
    expect(source.closed).toBe(true);
  });
});
