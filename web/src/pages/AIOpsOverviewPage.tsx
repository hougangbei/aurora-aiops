import { Typography } from 'antd';

export function AIOpsOverviewPage() {
  return (
    <section className="rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <Typography.Title level={2} className="!mb-3">
        智能运维 · 工作台
      </Typography.Title>
      <Typography.Paragraph className="!mb-0 text-slate-600">
        AIOps 诊断工作台总览，后续任务接入指标与入口。
      </Typography.Paragraph>
    </section>
  );
}
