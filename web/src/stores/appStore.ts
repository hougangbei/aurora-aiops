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
  // sessionMode 是历史枚举名，仅作为“演示数据/实时数据”兼容开关：
  // 'token' 只是历史命名，不再包含任何凭证，也不驱动认证。
  // 计划 03 将重命名为 dataMode: 'demo' | 'live'。
  sessionMode: 'demo' | 'token';
  setSession: (user: PlatformUser) => void;
  enterDemo: () => void;
  setUserName: (userName: string) => void;
  setSessionMode: (sessionMode: 'demo' | 'token') => void;
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
      sessionMode: 'demo',
      setSession: (user) =>
        set({
          authenticated: true,
          user,
          userName: user.username,
          sessionMode: 'token',
        }),
      enterDemo: () =>
        set({
          authenticated: true,
          user: { id: 'demo', username: '演示用户', role: 'viewer' },
          userName: '演示用户',
          namespace: 'default',
          sessionMode: 'demo',
        }),
      setUserName: (userName) => set({ userName }),
      setSessionMode: (sessionMode) => set({ sessionMode }),
      clearSession: () =>
        set({
          authenticated: false,
          user: null,
          namespace: 'default',
          sessionMode: 'demo',
          userName: '当前用户',
        }),
      setNamespace: (namespace) => set({ namespace }),
    }),
    {
      name: 'kubejojo-app',
    },
  ),
);
