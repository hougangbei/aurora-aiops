import { AppstoreOutlined, RightOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Empty, Skeleton, Space, Tag, Typography } from 'antd';
import { useNavigate } from 'react-router-dom';

import { listProjects } from '../modules/projects/api';
import type { Project } from '../modules/projects/types';
import { useAppStore } from '../stores/appStore';

function projectVersion(project: Project) {
  return project.recommendedVersion || project.versions[project.versions.length - 1] || '—';
}

export function AssetProjectsPage() {
  const navigate = useNavigate();
  const dataMode = useAppStore((state) => state.dataMode);
  const query = useQuery({ queryKey: ['projects'], queryFn: listProjects, enabled: dataMode === 'live' });
  const projects = query.data ?? [];

  return <section className="aurora-panel space-y-5 rounded-[24px] border p-6">
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div><Typography.Title level={2} className="!mb-1">项目中心</Typography.Title><Typography.Text type="secondary">浏览可安装的 Aurora 项目版本，并将项目安全部署到已登记服务器。</Typography.Text></div>
      <Tag icon={<AppstoreOutlined />} color="purple">Aurora 项目</Tag>
    </div>
    {dataMode === 'demo' ? <Alert type="info" showIcon message="演示模式为只读，项目安装需要切换到实时数据。" /> : null}
    {query.isError ? <Alert type="error" showIcon message="项目列表加载失败" description="请稍后刷新重试。" /> : null}
    {query.isLoading ? <div className="grid gap-4 md:grid-cols-2">{[1, 2].map((item) => <Card key={item}><Skeleton active /></Card>)}</div> : null}
    {!query.isLoading && projects.length === 0 ? <Empty description={dataMode === 'demo' ? '演示模式暂无项目数据' : '暂无可安装项目'} /> : null}
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      {projects.map((project) => <Card key={project.id} title={<Typography.Title level={4} className="!mb-0">{project.name}</Typography.Title>} extra={<Tag color="blue">{project.id}</Tag>} actions={[<Button aria-label="查看详情" key="details" type="link" icon={<RightOutlined />} onClick={() => navigate(`/assets/projects/${encodeURIComponent(project.id)}`)}>查看详情</Button>]}>
        <Space direction="vertical" size="middle" className="w-full">
          <Typography.Paragraph type="secondary" className="!mb-0">{project.description}</Typography.Paragraph>
          <Space wrap><Tag color="green">推荐版本：{projectVersion(project)}</Tag><Tag>已安装 {project.installedServerCount ?? 0} 台服务器</Tag></Space>
          <div><Typography.Text type="secondary">支持架构：</Typography.Text> <Space wrap>{project.supportedArchitectures.map((arch) => <Tag key={arch}>{arch}</Tag>)}</Space></div>
        </Space>
      </Card>)}
    </div>
  </section>;
}
