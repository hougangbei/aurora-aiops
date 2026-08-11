import { CloudDownloadOutlined, LinkOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Button, Card, Descriptions, Modal, Space, Spin, Table, Tabs, Tag, Typography } from 'antd';
import { useState } from 'react';
import { useParams } from 'react-router-dom';

import { collectAssetServer, confirmAssetHostKey, getAssetServer, getLatestAssetSnapshot, listAssetSoftware, testAssetConnection } from '../modules/assets/api';
import type { AssetServer } from '../modules/assets/types';
import { useAppStore } from '../stores/appStore';

const statusColor: Record<AssetServer['status'], string> = { pending: 'gold', online: 'green', offline: 'default', error: 'red' };

function fingerprintFromError(error: unknown): string | null {
  const response = (error as { response?: { status?: number; data?: { data?: { fingerprint?: unknown } } } })?.response;
  return response?.status === 409 && typeof response.data?.data?.fingerprint === 'string' ? response.data.data.fingerprint : null;
}

export function AssetServerDetailsPage() {
  const { id = '' } = useParams();
  const dataMode = useAppStore((state) => state.dataMode);
  const role = useAppStore((state) => state.user?.role);
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [pendingFingerprint, setPendingFingerprint] = useState<string | null>(null);
  const canOperate = dataMode === 'live' && (role === 'operator' || role === 'admin');
  const canConfirm = dataMode === 'live' && role === 'admin' && Boolean(pendingFingerprint);
  const enabled = dataMode === 'live' && Boolean(id);
  const serverQuery = useQuery({ queryKey: ['asset-servers', id], queryFn: () => getAssetServer(id), enabled });
  const snapshotQuery = useQuery({ queryKey: ['asset-servers', id, 'latest-snapshot'], queryFn: () => getLatestAssetSnapshot(id), enabled });
  const softwareQuery = useQuery({ queryKey: ['asset-servers', id, 'software'], queryFn: () => listAssetSoftware(id), enabled });
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['asset-servers'] });
    void queryClient.invalidateQueries({ queryKey: ['asset-servers', id] });
  };
  const handleAssetActionError = (error: unknown) => {
    const fingerprint = fingerprintFromError(error);
    if (fingerprint) setPendingFingerprint(fingerprint);
    else message.error('资产操作失败');
  };
  const testMutation = useMutation({
    mutationFn: () => testAssetConnection(id),
    onSuccess: () => { message.success('连接测试成功'); refresh(); },
    onError: handleAssetActionError,
  });
  const collectMutation = useMutation({
    mutationFn: () => collectAssetServer(id),
    onSuccess: () => { message.success('资产采集已完成'); refresh(); },
    onError: handleAssetActionError,
  });
  const confirmMutation = useMutation({
    mutationFn: () => confirmAssetHostKey(id, pendingFingerprint ?? ''),
    onSuccess: () => { setPendingFingerprint(null); message.success('SSH 主机密钥已确认'); refresh(); },
    onError: () => message.error('确认 SSH 主机密钥失败'),
  });

  if (serverQuery.isLoading) return <div className="flex justify-center py-24"><Spin size="large" /></div>;
  if (!serverQuery.data) return <Alert type="warning" showIcon message={dataMode === 'demo' ? '演示模式未加载服务器详情' : '服务器不存在'} />;
  const server = serverQuery.data;
  const snapshot = snapshotQuery.data;

  return (
    <section className="aurora-panel space-y-4 rounded-[24px] border p-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <Space size="middle"><Typography.Title level={2} className="!mb-0">{server.name}</Typography.Title><Tag color={statusColor[server.status]}>{server.status}</Tag></Space>
          <Typography.Text type="secondary">{server.username}@{server.address}:{server.sshPort}</Typography.Text>
        </div>
        <Space wrap>
          <Button aria-label="测试连接" icon={<LinkOutlined />} disabled={!canOperate || testMutation.isPending} loading={testMutation.isPending} onClick={() => testMutation.mutate()}>测试连接</Button>
          <Button aria-label="采集资产" icon={<CloudDownloadOutlined />} disabled={!canOperate || collectMutation.isPending} loading={collectMutation.isPending} onClick={() => collectMutation.mutate()}>采集资产</Button>
          <Button aria-label="确认主机密钥" icon={<SafetyCertificateOutlined />} disabled={!canConfirm || confirmMutation.isPending} loading={confirmMutation.isPending} onClick={() => confirmMutation.mutate()}>确认主机密钥</Button>
        </Space>
      </div>
      {!canOperate ? <Typography.Text type="secondary">{dataMode === 'demo' ? '演示模式为只读。' : '当前角色无权执行服务器操作。'}</Typography.Text> : null}
      {server.statusMessage ? <Alert type={server.status === 'error' ? 'error' : 'warning'} showIcon message={server.statusMessage} /> : null}
      {pendingFingerprint ? <Alert type="warning" showIcon message="SSH 主机密钥需要管理员确认" description={<code>{pendingFingerprint}</code>} /> : null}

      <Tabs items={[
        { key: 'overview', label: '概览', children: <Card size="small"><Descriptions column={{ xs: 1, md: 2 }} items={[
          { key: 'os', label: '操作系统', children: snapshot ? `${snapshot.osFamily} ${snapshot.osVersion}` : (server.osFamily || '尚未采集') },
          { key: 'arch', label: '架构', children: snapshot?.architecture || server.architecture || '—' },
          { key: 'last', label: '最近采集时间（可能已过期）', children: snapshot?.collectedAt ? new Date(snapshot.collectedAt).toLocaleString() : (server.lastCollectedAt ? new Date(server.lastCollectedAt).toLocaleString() : '尚未采集') },
          { key: 'cpu', label: 'CPU 核心', children: snapshot?.cpuCores ?? server.cpuCores },
        ]} /></Card> },
        { key: 'software', label: '软件', children: <Table rowKey={(item) => `${item.category}-${item.name}-${item.version}`} loading={softwareQuery.isLoading} dataSource={softwareQuery.data ?? []} pagination={false} columns={[
          { title: '类别', dataIndex: 'category' }, { title: '名称', dataIndex: 'name' }, { title: '版本', dataIndex: 'version' }, { title: '来源', dataIndex: 'source' }, { title: '状态', dataIndex: 'status' },
        ]} /> },
        { key: 'installations', label: '安装记录', children: <Alert type="info" showIcon message="部署任务功能将在下一阶段启用" /> },
        { key: 'tasks', label: '任务进度', children: <Alert type="info" showIcon message="部署任务功能将在下一阶段启用" /> },
      ]} />
      <Modal open={Boolean(pendingFingerprint)} title="确认 SSH 主机密钥" okText="确认并信任" cancelText="取消" onCancel={() => setPendingFingerprint(null)} onOk={() => confirmMutation.mutate()} okButtonProps={{ disabled: role !== 'admin' }} confirmLoading={confirmMutation.isPending}>
        <p>请核对指纹后再确认：</p><code>{pendingFingerprint}</code>
      </Modal>
    </section>
  );
}
