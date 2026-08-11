import { useMutation, useQueryClient } from '@tanstack/react-query';
import { App, Button, Drawer, Form, Input, InputNumber, Modal, Radio, Switch } from 'antd';
import { useState } from 'react';

import { confirmAssetHostKey, createAssetServer } from '../api';
import type { AssetServer, CreateAssetServerInput } from '../types';

type HostKeyConfirmation = { fingerprint: string; server: AssetServer };

type AddServerDrawerProps = {
  open: boolean;
  onClose: () => void;
  onCreated?: (server: AssetServer) => void;
};

function hostKeyConfirmation(error: unknown): HostKeyConfirmation | null {
  const response = (error as { response?: { status?: number; data?: { data?: unknown } } })?.response;
  const data = response?.data?.data as Partial<HostKeyConfirmation> | undefined;
  if (response?.status === 409 && typeof data?.fingerprint === 'string' && data.server?.id) {
    return { fingerprint: data.fingerprint, server: data.server };
  }
  return null;
}

export function AddServerDrawer({ open, onClose, onCreated }: AddServerDrawerProps) {
  const [form] = Form.useForm<CreateAssetServerInput>();
  const authType = Form.useWatch('credentialAuthType', form) ?? 'password';
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [confirmation, setConfirmation] = useState<HostKeyConfirmation | null>(null);

  const refreshServers = () => void queryClient.invalidateQueries({ queryKey: ['asset-servers'] });
  const clearAndClose = () => {
    form.resetFields();
    onClose();
  };

  const createMutation = useMutation({
    mutationFn: createAssetServer,
    onSuccess: (server) => {
      refreshServers();
      onCreated?.(server);
      message.success('服务器已保存');
      clearAndClose();
    },
    onError: (error) => {
      const hostKey = hostKeyConfirmation(error);
      if (hostKey) {
        // A 409 can still carry a safely-created pending server. Keep its response free of credentials.
        refreshServers();
        onCreated?.(hostKey.server);
        form.resetFields();
        onClose();
        setConfirmation(hostKey);
        return;
      }
      message.error('保存服务器失败');
    },
  });

  const confirmMutation = useMutation({
    mutationFn: ({ id, fingerprint }: { id: string; fingerprint: string }) => confirmAssetHostKey(id, fingerprint),
    onSuccess: () => {
      refreshServers();
      setConfirmation(null);
      message.success('SSH 主机密钥已确认');
    },
    onError: () => message.error('确认 SSH 主机密钥失败'),
  });

  const submit = (values: CreateAssetServerInput) => {
    const input: CreateAssetServerInput = {
      name: values.name,
      address: values.address,
      username: values.username,
      sshPort: values.sshPort,
      credentialAuthType: values.credentialAuthType,
      testConnection: values.testConnection,
    };
    if (values.credentialAuthType === 'password') {
      input.password = values.password;
    } else {
      input.privateKey = values.privateKey;
      input.passphrase = values.passphrase;
    }
    createMutation.mutate(input);
  };

  return (
    <>
      <Drawer
        title="新增服务器"
        open={open}
        width={480}
        destroyOnHidden
        onClose={clearAndClose}
        extra={<Button type="primary" loading={createMutation.isPending} onClick={() => form.submit()}>保存服务器</Button>}
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ sshPort: 22, credentialAuthType: 'password', testConnection: true }}
          onFinish={submit}
        >
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入服务器名称' }]}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="address" label="地址" rules={[{ required: true, message: '请输入服务器地址' }]}>
            <Input autoComplete="off" placeholder="192.0.2.10 或 server.example.com" />
          </Form.Item>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入 SSH 用户名' }]}>
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item name="sshPort" label="SSH 端口" rules={[{ required: true, type: 'number', min: 1, max: 65535 }]}>
            <InputNumber className="!w-full" min={1} max={65535} />
          </Form.Item>
          <Form.Item name="credentialAuthType" label="认证方式" rules={[{ required: true }]}>
            <Radio.Group>
              <Radio value="password">密码</Radio>
              <Radio value="private_key">私钥</Radio>
            </Radio.Group>
          </Form.Item>
          {authType === 'password' ? (
            <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
              <Input.Password autoComplete="new-password" />
            </Form.Item>
          ) : (
            <>
              <Form.Item name="privateKey" label="私钥" rules={[{ required: true, message: '请输入私钥' }]}>
                <Input.Password autoComplete="new-password" />
              </Form.Item>
              <Form.Item name="passphrase" label="私钥口令">
                <Input.Password autoComplete="new-password" />
              </Form.Item>
            </>
          )}
          <Form.Item name="testConnection" label="保存前测试连接" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Drawer>

      <Modal
        open={Boolean(confirmation)}
        title="确认 SSH 主机密钥"
        okText="确认并信任"
        cancelText="取消"
        confirmLoading={confirmMutation.isPending}
        onCancel={() => setConfirmation(null)}
        onOk={() => {
          if (confirmation) {
            confirmMutation.mutate({ id: confirmation.server.id, fingerprint: confirmation.fingerprint });
          }
        }}
      >
        <p>服务器已创建为待连接状态。请核对 SSH 主机密钥指纹后再明确确认：</p>
        <code>{confirmation?.fingerprint}</code>
      </Modal>
    </>
  );
}
