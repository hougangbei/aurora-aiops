import { Typography } from 'antd';

export function AIOpsAuditPage() {
  return (
    <section className="rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <Typography.Title level={2} className="!mb-3">
        操作审计
      </Typography.Title>
      <Typography.Paragraph className="!mb-0 text-slate-600">
        操作审计与证据校验，后续任务接入。
      </Typography.Paragraph>
    </section>
  );
}
