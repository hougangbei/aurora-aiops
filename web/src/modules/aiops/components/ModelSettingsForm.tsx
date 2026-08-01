import { App, Button, Form, Input, Select, Typography } from 'antd';
import { useState } from 'react';

// 模型设置表单：API Key 永不回显，只显示掩码；空 Key 表示保留原值。
export function ModelSettingsForm({ configuredKey = false }: { configuredKey?: boolean }) {
  const { message } = App.useApp();
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);

  const submit = () => {
    // 后端模型配置接口待接入（计划 02 reference / 后续计划）。
    setSaving(true);
    window.setTimeout(() => {
      setSaving(false);
      form.resetFields(['apiKey']);
      message.success('配置已保存（后端配置接口待接入）');
    }, 300);
  };

  return (
    <Form form={form} layout="vertical" initialValues={{ style: 'chat_completions' }} onFinish={submit}>
      <Form.Item
        name="baseUrl"
        label="Base URL"
        rules={[{ required: true, message: '请输入 Base URL' }]}
        extra="仅允许 HTTPS 端点。"
      >
        <Input placeholder="https://api.example.com/v1" />
      </Form.Item>
      <Form.Item name="model" label="模型">
        <Input placeholder="model-name" />
      </Form.Item>
      <Form.Item name="style" label="API 风格">
        <Select
          options={[
            { value: 'chat_completions', label: 'chat/completions' },
            { value: 'responses', label: 'responses' },
          ]}
        />
      </Form.Item>
      <Form.Item name="apiKey" label={configuredKey ? 'API Key（已配置）' : 'API Key'}>
        <Input.Password
          placeholder={configuredKey ? '••••••••（已配置，留空保留原值）' : 'sk-...'}
          autoComplete="new-password"
        />
      </Form.Item>
      {configuredKey ? (
        <Typography.Text type="secondary" className="!mb-4 block">
          后端只返回掩码，前端不会加载真实密钥。留空保存表示保留原值。
        </Typography.Text>
      ) : null}
      <Button type="primary" htmlType="submit" loading={saving}>
        保存
      </Button>
    </Form>
  );
}
