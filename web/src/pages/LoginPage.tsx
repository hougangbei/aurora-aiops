import { useRef } from 'react';
import type { PointerEvent as ReactPointerEvent } from 'react';

import { App, Alert, Button, Form, Input } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { AxiosError } from 'axios';
import { useNavigate } from 'react-router-dom';

import { loginWithPassword } from '../services/cluster';
import { useAppStore } from '../stores/appStore';

import './LoginPage.css';

type LoginFormValues = {
  username: string;
  password: string;
};

export function LoginPage() {
  const stageRef = useRef<HTMLDivElement>(null);
  const { message } = App.useApp();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const setSession = useAppStore((state) => state.setSession);
  const enterDemo = useAppStore((state) => state.enterDemo);

  const loginMutation = useMutation({
    mutationFn: ({ username, password }: LoginFormValues) =>
      loginWithPassword(username, password),
    onSuccess: (user) => {
      queryClient.clear();
      setSession(user);
      navigate('/cluster/overview', { replace: true });
      void message.success('已登录并接入集群控制台');
    },
  });

  const handleFinish = (values: LoginFormValues) => {
    loginMutation.mutate(values);
  };

  const handleDemoEnter = () => {
    queryClient.clear();
    enterDemo();
    navigate('/cluster/overview', { replace: true });
  };

  const handlePointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (
      event.pointerType === 'touch' ||
      (typeof window !== 'undefined' &&
        window.matchMedia('(prefers-reduced-motion: reduce)').matches)
    ) {
      return;
    }

    const rect = event.currentTarget.getBoundingClientRect();
    event.currentTarget.style.setProperty('--pointer-x', `${event.clientX - rect.left}px`);
    event.currentTarget.style.setProperty('--pointer-y', `${event.clientY - rect.top}px`);
  };

  const handlePointerLeave = () => {
    stageRef.current?.style.setProperty('--pointer-x', '50%');
    stageRef.current?.style.setProperty('--pointer-y', '42%');
  };

  const errorMessage =
    loginMutation.error instanceof AxiosError
      ? loginMutation.error.response?.data?.message ?? '登录失败，请检查账号密码'
      : loginMutation.error
        ? '登录失败，请稍后重试'
        : '';

  return (
    <main className="login-page">
      <div
        ref={stageRef}
        className="login-stage"
        onPointerMove={handlePointerMove}
        onPointerLeave={handlePointerLeave}
      >
        <div className="login-cursor-glow" aria-hidden="true" />
        <div className="login-scanlines" aria-hidden="true" />

        <section className="login-panel" aria-labelledby="login-title">
          <header className="login-panel-header">
            <p className="login-node-label">SYSTEM NODE: AURORA-CORE</p>
            <div className="login-mark" aria-hidden="true">
              A
            </div>
            <h1 className="login-title" id="login-title">
              AURORA AIOPS
              <br />
              ACCESS CONTROL
            </h1>
            <p className="login-subtitle">
              Aurora AIOps 智能运维平台安全访问入口。
              <br />
              Authenticate to continue into the Aurora control plane.
            </p>
          </header>

          {errorMessage ? (
            <Alert
              showIcon
              type="error"
              className="login-error"
              message={errorMessage}
            />
          ) : null}

          <Form layout="vertical" onFinish={handleFinish} className="login-form">
            <Form.Item
              className="login-field login-field-shell"
              htmlFor="login-username"
              label="用户身份 / User Identity"
              name="username"
              rules={[{ required: true, message: '请输入用户名' }]}
            >
              <Input
                id="login-username"
                className="login-input"
                prefix={
                  <span className="login-field-mark" aria-hidden="true">
                    ID
                  </span>
                }
                placeholder="请输入平台用户名"
                autoComplete="username"
              />
            </Form.Item>

            <Form.Item
              className="login-field login-field-shell"
              htmlFor="login-password"
              label="序列密钥 / Sequence Key"
              name="password"
              rules={[{ required: true, message: '请输入密码' }]}
            >
              <Input.Password
                id="login-password"
                className="login-input"
                prefix={
                  <span className="login-field-mark" aria-hidden="true">
                    KEY
                  </span>
                }
                placeholder="请输入密码"
                autoComplete="current-password"
              />
            </Form.Item>

            <div className="login-actions">
              <Button
                type="primary"
                htmlType="submit"
                block
                className="login-submit"
                loading={loginMutation.isPending}
              >
                INITIALIZE SESSION ↗
              </Button>
              <Button
                block
                className="login-demo"
                onClick={handleDemoEnter}
                disabled={loginMutation.isPending}
              >
                进入演示模式
              </Button>
            </div>
          </Form>

          <footer className="login-footer">
            <span>ENCRYPTED CHANNEL</span>
            <span>AURORA AIOPS · V1.0.0</span>
          </footer>
        </section>
      </div>
    </main>
  );
}
