import { Typography } from 'antd';

export function AIOpsSettingsPage() {
  return (
    <section className="rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <Typography.Title level={2} className="!mb-3">
        模型设置
      </Typography.Title>
      <Typography.Paragraph className="!mb-0 text-slate-600">
        模型端点与集成配置，后续任务接入安全表单。
      </Typography.Paragraph>
    </section>
  );
}
