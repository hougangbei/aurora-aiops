import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntdApp, ConfigProvider } from 'antd';
import { PropsWithChildren, ReactElement } from 'react';
import { render, RenderOptions } from '@testing-library/react';

// createTestQueryClient returns a QueryClient that never retries, so tests are
// deterministic instead of stalling on transient query failures.
export function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  });
}

type RenderWithProvidersOptions = {
  queryClient?: QueryClient;
} & Omit<RenderOptions, 'wrapper'>;

// renderWithProviders renders a component wrapped in the same providers the
// application uses (React Query + antd), with a fresh non-retrying client. The
// client is returned so tests can seed or assert cached data.
export function renderWithProviders(ui: ReactElement, options: RenderWithProvidersOptions = {}) {
  const { queryClient = createTestQueryClient(), ...renderOptions } = options;

  function Wrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={queryClient}>
        <ConfigProvider>
          <AntdApp>{children}</AntdApp>
        </ConfigProvider>
      </QueryClientProvider>
    );
  }

  return { ...render(ui, { wrapper: Wrapper, ...renderOptions }), queryClient };
}
