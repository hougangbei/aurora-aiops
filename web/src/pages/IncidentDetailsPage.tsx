import { Typography } from 'antd';
import { useParams } from 'react-router-dom';

export function IncidentDetailsPage() {
  const { id } = useParams();
  return (
    <section className="rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <Typography.Title level={2} className="!mb-3">
        Incident 详情
      </Typography.Title>
      <Typography.Paragraph className="!mb-0 text-slate-600">
        Incident {id} 的诊断阶段、证据与修复方案，后续任务接入实时视图。
      </Typography.Paragraph>
    </section>
  );
}
