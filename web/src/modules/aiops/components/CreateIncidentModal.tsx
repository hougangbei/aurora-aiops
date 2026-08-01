import { useMutation, useQueryClient } from '@tanstack/react-query';
import { App, Form, Input, Modal, Select } from 'antd';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { createIncident } from '../api';
import type { CreateIncidentInput, IncidentSeverity } from '../types';

// 创建 Incident 只允许这几类资源。
const resourceKinds = ['Pod', 'Deployment', 'Service', 'PVC'];

const severityOptions: { value: IncidentSeverity; label: string }[] = [
  { value: 'info', label: 'info' },
  { value: 'warning', label: 'warning' },
  { value: 'critical', label: 'critical' },
];

export function CreateIncidentModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [form] = Form.useForm<CreateIncidentInput>();
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);

  const createMutation = useMutation({
    mutationFn: createIncident,
    onSuccess: (incident) => {
      void queryClient.invalidateQueries({ queryKey: ['aiops', 'incidents'] });
      message.success('已创建诊断任务');
      form.resetFields();
      onClose();
      navigate(`/aiops/incidents/${incident.id}`);
    },
    onError: (error) => {
      message.error(`创建失败：${String(error)}`);
      setSubmitting(false);
    },
  });

  const submit = () => {
    form
      .validateFields()
      .then((values) => {
        setSubmitting(true);
        createMutation.mutate(values);
      })
      .catch(() => {
        // 表单校验失败，antd 已展示错误。
      });
  };

  return (
    <Modal
      title="创建诊断任务"
      open={open}
      onOk={submit}
      onCancel={onClose}
      okText="创建"
      cancelText="取消"
      confirmLoading={submitting}
      destroyOnClose
    >
      <Form form={form} layout="vertical" initialValues={{ severity: 'warning', resourceKind: 'Pod' }}>
        <Form.Item name="summary" label="摘要" rules={[{ required: true, message: '请输入摘要' }]}>
          <Input placeholder="告警摘要" />
        </Form.Item>
        <Form.Item name="severity" label="严重度" rules={[{ required: true }]}>
          <Select options={severityOptions} />
        </Form.Item>
        <Form.Item name="namespace" label="命名空间" rules={[{ required: true, message: '请输入命名空间' }]}>
          <Input placeholder="default" />
        </Form.Item>
        <Form.Item name="resourceKind" label="资源类型" rules={[{ required: true }]}>
          <Select options={resourceKinds.map((kind) => ({ value: kind, label: kind }))} />
        </Form.Item>
        <Form.Item name="resourceName" label="资源名称" rules={[{ required: true, message: '请输入资源名称' }]}>
          <Input placeholder="api-0" />
        </Form.Item>
      </Form>
    </Modal>
  );
}
