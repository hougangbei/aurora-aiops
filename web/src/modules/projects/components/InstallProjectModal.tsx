import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Checkbox, Form, Input, Modal, Select, Space, Tag, Typography } from 'antd';
import { useEffect, useMemo, useState } from 'react';

import type { AssetServer } from '../../assets/types';
import { TaskProgressDrawer } from '../../deployment/components/TaskProgressDrawer';
import { installProject } from '../api';
import type { InstallProjectInput, Project } from '../types';

type Props = {
  open: boolean;
  project: Project;
  servers: AssetServer[];
  canInstall: boolean;
  disabledReason?: string;
  onClose: () => void;
};

type FormValues = {
  serverId?: string;
  version?: string;
  bootstrapAdminUser?: string;
  bootstrapAdminPassword?: string;
  confirmBootstrapAdminPassword?: string;
  confirmed?: boolean;
};

function serverReason(project: Project, server: AssetServer): string | null {
  if (server.status !== 'online') return '服务器不在线';
  if (server.hostKeyConfirmed === false) return 'SSH 主机密钥尚未确认';
  if (project.supportedOsFamilies.length > 0 && !project.supportedOsFamilies.includes(server.osFamily ?? '')) return '操作系统不兼容';
  if (project.supportedArchitectures.length > 0 && !project.supportedArchitectures.includes(server.architecture ?? '')) return '架构不兼容';
  return null;
}

export function InstallProjectModal({ open, project, servers, canInstall, disabledReason, onClose }: Props) {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [form] = Form.useForm<FormValues>();
  const [taskId, setTaskId] = useState<string>();
  const [errorMessage, setErrorMessage] = useState<string>();
  const [showProgress, setShowProgress] = useState(false);
  const recommendedVersion = project.recommendedVersion || project.versions[project.versions.length - 1] || '';
  const serverOptions = useMemo(() => servers.map((server) => ({ server, reason: serverReason(project, server) })), [project, servers]);
  const firstCompatible = serverOptions.find((item) => !item.reason)?.server.id;

  useEffect(() => {
    if (!open) {
      form.resetFields();
      setErrorMessage(undefined);
    } else if (!form.getFieldValue('version')) {
      form.setFieldsValue({ version: recommendedVersion, serverId: firstCompatible });
    }
  }, [open, form, firstCompatible, recommendedVersion]);

  const mutation = useMutation({
    mutationFn: (input: InstallProjectInput) => installProject(project.id, input),
    onSuccess: (task) => {
      setTaskId(task.id);
      setShowProgress(true);
      setErrorMessage(undefined);
      message.success('安装任务已提交');
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
      void queryClient.invalidateQueries({ queryKey: ['project', project.id] });
      void queryClient.invalidateQueries({ queryKey: ['asset-servers'] });
    },
    onError: (error: unknown) => {
      const response = (error as { response?: { status?: number; data?: { code?: string; message?: string } } }).response;
      if (response?.status === 409 || response?.data?.code === 'DEPLOYMENT_ACTIVE_TASK') setErrorMessage('该服务器已有进行中的安装任务，请先等待完成或取消后重试。');
      else setErrorMessage(response?.data?.message || '安装任务提交失败，请检查目标服务器和配置。');
    },
  });

  const submit = async (values: FormValues) => {
    if (!canInstall) return;
    if (values.bootstrapAdminPassword !== values.confirmBootstrapAdminPassword) {
      form.setFields([{ name: 'confirmBootstrapAdminPassword', errors: ['两次输入的密码不一致'] }]);
      return;
    }
    await mutation.mutateAsync({
      serverId: values.serverId || '',
      version: values.version || recommendedVersion,
      configuration: { bootstrapAdminUser: values.bootstrapAdminUser || '', bootstrapAdminPassword: values.bootstrapAdminPassword || '' },
    });
  };

  return <>
    <Modal open={open} title={`安装 ${project.name}`} okText="开始安装" cancelText="取消" cancelButtonProps={{ 'aria-label': '取消' }} okButtonProps={{ 'aria-label': '开始安装', disabled: !canInstall || mutation.isPending }} onCancel={onClose} onOk={() => form.submit()} confirmLoading={mutation.isPending} destroyOnHidden>
      <Typography.Paragraph type="secondary">选择兼容的在线服务器，并设置首次登录的管理员账号。密码仅用于本次安装配置，不会显示在任务日志中。</Typography.Paragraph>
      {!canInstall && disabledReason ? <Alert type="warning" showIcon message={disabledReason} className="mb-4" /> : null}
      {errorMessage ? <Alert type="error" showIcon message={errorMessage} className="mb-4" /> : null}
      <Form form={form} layout="vertical" onFinish={submit} requiredMark="optional">
        <Form.Item label="目标服务器" name="serverId" rules={[{ required: true, message: '请选择目标服务器' }]}>
          <Select aria-label="目标服务器" placeholder="选择服务器" options={serverOptions.map(({ server, reason }) => ({ value: server.id, disabled: Boolean(reason), label: <Space><span>{server.name} · {server.address}</span>{reason ? <Tag color="warning">{reason}</Tag> : <Tag color="success">可安装</Tag>}</Space> }))} />
        </Form.Item>
        <Space direction="vertical" size={2} className="-mt-3 mb-3 w-full">
          {serverOptions.filter(({ reason }) => reason).map(({ server, reason }) => <Typography.Text key={server.id} type="secondary">{server.name}：{reason}</Typography.Text>)}
        </Space>
        <Form.Item label="版本" name="version" rules={[{ required: true, message: '请选择版本' }]}>
          <Select aria-label="版本" options={project.versions.map((version) => ({ value: version, label: version === recommendedVersion ? `${version}（推荐）` : version }))} />
        </Form.Item>
        <Form.Item label="初始管理员用户名" name="bootstrapAdminUser" rules={[{ required: true, min: 3, max: 64, message: '请输入 3–64 个字符的管理员用户名' }]}>
          <Input aria-label="初始管理员用户名" autoComplete="off" />
        </Form.Item>
        <Form.Item label="初始管理员密码" name="bootstrapAdminPassword" rules={[{ required: true, min: 12, max: 128, message: '请输入 12–128 个字符的初始密码' }]}>
          <Input.Password aria-label="初始管理员密码" autoComplete="new-password" />
        </Form.Item>
        <Form.Item label="确认初始管理员密码" name="confirmBootstrapAdminPassword" rules={[{ required: true, message: '请再次输入初始密码' }]}>
          <Input.Password aria-label="确认初始管理员密码" autoComplete="new-password" />
        </Form.Item>
        <Form.Item name="confirmed" valuePropName="checked" rules={[{ validator: (_, value) => value ? Promise.resolve() : Promise.reject(new Error('请确认安装摘要')) }]}>
          <Checkbox aria-label="我确认在目标服务器安装 Aurora AIOps">我确认在目标服务器安装 Aurora AIOps</Checkbox>
        </Form.Item>
      </Form>
      <Typography.Paragraph type="secondary" className="!mb-0">安装摘要：{project.name} {recommendedVersion} → {firstCompatible ? servers.find((server) => server.id === firstCompatible)?.name : '未选择服务器'}</Typography.Paragraph>
    </Modal>
    <TaskProgressDrawer taskId={taskId} open={showProgress} canManage={canInstall} onClose={() => setShowProgress(false)} />
  </>;
}
