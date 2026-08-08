import { Card, Typography } from 'antd';

import { ModelSettingsForm } from '../modules/aiops/components/ModelSettingsForm';

export function AIOpsSettingsPage() {
  return (
    <section className="aurora-panel space-y-4 rounded-[24px] border p-6">
      <div>
        <Typography.Title level={2} className="!mb-1">
          模型设置
        </Typography.Title>
        <Typography.Text type="secondary">模型端点与集成配置。API Key 永不回显，空 Key 表示保留原值。</Typography.Text>
      </div>

      <Card size="small" title="OpenAI-compatible 端点" className="max-w-xl">
        <ModelSettingsForm configuredKey />
      </Card>
    </section>
  );
}
