import { Typography } from 'antd';

export function AIOpsApprovalsPage() {
  return (
    <section className="rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <Typography.Title level={2} className="!mb-3">
        修复审批
      </Typography.Title>
      <Typography.Paragraph className="!mb-0 text-slate-600">
        修复方案审批与执行决策，后续任务接入审批规则。
      </Typography.Paragraph>
    </section>
  );
}
