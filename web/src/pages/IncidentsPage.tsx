import { Typography } from 'antd';

export function IncidentsPage() {
  return (
    <section className="rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <Typography.Title level={2} className="!mb-3">
        Incident 列表
      </Typography.Title>
      <Typography.Paragraph className="!mb-0 text-slate-600">
        诊断任务列表、创建与筛选，后续任务接入表格与创建表单。
      </Typography.Paragraph>
    </section>
  );
}
