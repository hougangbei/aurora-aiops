import { Typography } from 'antd';

export function AIOpsExperimentsPage() {
  return (
    <section className="rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <Typography.Title level={2} className="!mb-3">
        故障实验
      </Typography.Title>
      <Typography.Paragraph className="!mb-0 text-slate-600">
        故障注入与演练实验，后续任务接入。
      </Typography.Paragraph>
    </section>
  );
}
