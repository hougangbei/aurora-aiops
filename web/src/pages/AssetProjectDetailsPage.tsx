import { ArrowLeftOutlined, CloudDownloadOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Descriptions, Empty, Spin, Table, Tag, Typography } from 'antd';
import { useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';

import { listAssetServers } from '../modules/assets/api';
import { TaskProgressDrawer } from '../modules/deployment/components/TaskProgressDrawer';
import { InstallProjectModal } from '../modules/projects/components/InstallProjectModal';
import { getProject } from '../modules/projects/api';
import type { ProjectInstallation } from '../modules/projects/types';
import { useAppStore } from '../stores/appStore';

const statusLabel: Record<string, string> = { succeeded: '成功', failed: '失败', queued: '排队中', running: '执行中', cancelled: '已取消' };

export function AssetProjectDetailsPage() {
  const { projectId = '' } = useParams();
  const navigate = useNavigate();
  const dataMode = useAppStore((state) => state.dataMode);
  const role = useAppStore((state) => state.user?.role);
  const [installOpen, setInstallOpen] = useState(false);
  const [searchParams, setSearchParams] = useSearchParams();
  const projectQuery = useQuery({ queryKey: ['project', projectId], queryFn: () => getProject(projectId), enabled: dataMode === 'live' && Boolean(projectId) });
  const serversQuery = useQuery({ queryKey: ['asset-servers'], queryFn: listAssetServers, enabled: dataMode === 'live' && installOpen });
  const project = projectQuery.data;
  const canInstall = dataMode === 'live' && role === 'admin';
  const selectedTaskId = searchParams.get('task');
  const handleTaskCreated = (taskId: string) => {
    const next = new URLSearchParams(searchParams);
    next.set('task', taskId);
    setSearchParams(next);
    setInstallOpen(false);
  };
  const closeTask = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('task');
    setSearchParams(next);
  };

  if (projectQuery.isLoading) return <div className="flex justify-center py-24"><Spin size="large" /></div>;
  if (!project) return <Alert type="warning" showIcon message={dataMode === 'demo' ? '演示模式未加载项目详情' : '项目不存在'} />;
  const installations = project.installations ?? [];
  const columns = [
    { title: '服务器', key: 'server', render: (_: unknown, item: ProjectInstallation) => item.serverName || item.serverId },
    { title: '版本', dataIndex: 'version', key: 'version' },
    { title: '状态', dataIndex: 'status', key: 'status', render: (status: string) => <Tag color={status === 'succeeded' ? 'success' : status === 'failed' ? 'error' : 'processing'}>{statusLabel[status] || status}</Tag> },
    { title: '完成时间', dataIndex: 'finishedAt', key: 'finishedAt', render: (value?: string) => value ? new Date(value).toLocaleString() : '—' },
  ];
  return <section className="aurora-panel space-y-5 rounded-[24px] border p-6">
    <Button type="link" icon={<ArrowLeftOutlined />} onClick={() => navigate('/assets/projects')}>返回项目中心</Button>
    <div className="flex flex-wrap items-start justify-between gap-4"><div><Typography.Title level={2} className="!mb-1">{project.name}</Typography.Title><Typography.Text type="secondary">{project.description}</Typography.Text></div><Button aria-label="安装到服务器" type="primary" icon={<CloudDownloadOutlined />} disabled={!canInstall} onClick={() => setInstallOpen(true)}>安装到服务器</Button></div>
    {!canInstall ? <Typography.Text type="secondary">{dataMode === 'demo' ? '演示模式为只读。' : '当前角色无权安装项目。'}</Typography.Text> : null}
    {project.id === 'kubernetes' ? <Alert type="warning" showIcon message="Kubernetes 一键安装仅适用于未初始化的受支持 Linux 主机" description="检测到已有 kubeadm 集群时会在任何变更前停止并标记为需要只读接管；不会执行 reset 或卸载。" /> : null}
    <Card title="项目能力"><Descriptions column={{ xs: 1, md: 2 }} items={[{ key: 'versions', label: '支持版本', children: <span>{project.versions.join('、')}</span> }, { key: 'recommended', label: '推荐版本', children: <Tag color="green">{project.recommendedVersion || project.versions[project.versions.length - 1]}</Tag> }, { key: 'os', label: '支持操作系统', children: project.supportedOsFamilies.join('、') || '—' }, { key: 'arch', label: '支持架构', children: project.supportedArchitectures.join('、') || '—' }]} /></Card>
    <Card title="安装历史"><Table rowKey={(item) => item.id || `${item.serverId}-${item.version}-${item.finishedAt}`} columns={columns} dataSource={installations} pagination={false} locale={{ emptyText: '暂无安装记录' }} /></Card>
    <InstallProjectModal open={installOpen} project={project} servers={serversQuery.data ?? []} canInstall={canInstall} disabledReason={dataMode === 'demo' ? '演示模式为只读。' : '当前角色无权安装项目。'} onTaskCreated={handleTaskCreated} onClose={() => setInstallOpen(false)} />
    <TaskProgressDrawer taskId={selectedTaskId ?? undefined} open={Boolean(selectedTaskId)} canManage={canInstall} onClose={closeTask} />
  </section>;
}
