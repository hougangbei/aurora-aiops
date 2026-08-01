import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export type PlatformUser = {
  id: string;
  username: string;
  role: string;
};

type AppState = {
  authenticated: boolean;
  user: PlatformUser | null;
  namespace: string;
  userName: string;
  // dataMode 是“演示数据/实时数据”兼容开关：'demo' 展示内置数据，
  // 'live' 才走真实 API。不再包含任何凭证，也不驱动认证。
  dataMode: 'demo' | 'live';
  setSession: (user: PlatformUser) => void;
  enterDemo: () => void;
  setUserName: (userName: string) => void;
  setDataMode: (dataMode: 'demo' | 'live') => void;
  clearSession: () => void;
  setNamespace: (namespace: string) => void;
};

export const useAppStore = create<AppState>()(
  persist(
    (set) => ({
      authenticated: false,
      user: null,
      namespace: 'default',
      userName: '当前用户',
      dataMode: 'demo',
      setSession: (user) =>
        set({
          authenticated: true,
          user,
          userName: user.username,
          dataMode: 'live',
        }),
      enterDemo: () =>
        set({
          authenticated: true,
          user: { id: 'demo', username: '演示用户', role: 'viewer' },
          userName: '演示用户',
          namespace: 'default',
          dataMode: 'demo',
        }),
      setUserName: (userName) => set({ userName }),
      setDataMode: (dataMode) => set({ dataMode }),
      clearSession: () =>
        set({
          authenticated: false,
          user: null,
          namespace: 'default',
          dataMode: 'demo',
          userName: '当前用户',
        }),
      setNamespace: (namespace) => set({ namespace }),
    }),
    {
      name: 'kubejojo-app',
    },
  ),
);
