import { PlusOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Card, Input, Space, Statistic, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { listAssetServers } from '../modules/assets/api';
import { AddServerDrawer } from '../modules/assets/components/AddServerDrawer';
import type { AssetServer } from '../modules/assets/types';
import { useAppStore } from '../stores/appStore';

const statusColor: Record<AssetServer['status'], string> = {
  pending: 'gold', online: 'green', offline: 'default', error: 'red',
};

export function AssetServersPage() {
  const dataMode = useAppStore((state) => state.dataMode);
  const role = useAppStore((state) => state.user?.role);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [search, setSearch] = useState('');
  const canCreate = dataMode === 'live' && role === 'admin';
  const serversQuery = useQuery({
    queryKey: ['asset-servers'],
    queryFn: listAssetServers,
    enabled: dataMode === 'live',
  });
  const servers = serversQuery.data ?? [];
  const filteredServers = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    if (!keyword) return servers;
    return servers.filter((server) => [server.name, server.address, server.username]
      .some((value) => value.toLowerCase().includes(keyword)));
  }, [search, servers]);
  const metrics = useMemo(() => ({
    total: servers.length,
    online: servers.filter((server) => server.status === 'online').length,
    pending: servers.filter((server) => server.status === 'pending').length,
    unavailable: servers.filter((server) => server.status === 'offline' || server.status === 'error').length,
  }), [servers]);
  const columns: ColumnsType<AssetServer> = [
    { title: '名称', dataIndex: 'name', key: 'name', render: (name: string, server) => <div><strong>{name}</strong><br /><Typography.Text type="secondary">{server.address}:{server.sshPort}</Typography.Text></div> },
    { title: '用户', dataIndex: 'username', key: 'username' },
    { title: '状态', dataIndex: 'status', key: 'status', render: (status: AssetServer['status'], server) => <Space direction="vertical" size={0}><Tag color={statusColor[status]}>{status}</Tag>{server.statusMessage ? <Typography.Text type="secondary">{server.statusMessage}</Typography.Text> : null}</Space> },
    { title: '系统', key: 'os', render: (_, server) => server.osFamily ? `${server.osFamily} ${server.osVersion ?? ''}` : '待采集' },
    { title: '最近采集', key: 'collected', render: (_, server) => server.lastCollectedAt ? new Date(server.lastCollectedAt).toLocaleString() : '尚未采集' },
  ];

  return (
    <section className="aurora-panel space-y-4 rounded-[24px] border p-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <Typography.Title level={2} className="!mb-1">服务器资产</Typography.Title>
          <Typography.Text type="secondary">统一查看已登记服务器及其最新资产采集状态。</Typography.Text>
        </div>
        <Button aria-label="新增服务器" type="primary" icon={<PlusOutlined />} disabled={!canCreate} onClick={() => setDrawerOpen(true)}>新增服务器</Button>
      </div>
      {!canCreate ? <Typography.Text type="secondary">{dataMode === 'demo' ? '演示模式为只读。' : '当前角色为只读，无法新增服务器。'}</Typography.Text> : null}

      <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
        <Card size="small"><Statistic title="总数" value={metrics.total} /></Card>
        <Card size="small"><Statistic title="在线" value={metrics.online} valueStyle={{ color: '#15803d' }} /></Card>
        <Card size="small"><Statistic title="待连接" value={metrics.pending} valueStyle={{ color: '#d97706' }} /></Card>
        <Card size="small"><Statistic title="离线/错误" value={metrics.unavailable} valueStyle={{ color: '#dc2626' }} /></Card>
      </div>

      <Input.Search allowClear value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索名称、地址或用户名" />
      <Table
        rowKey="id"
        loading={serversQuery.isLoading}
        columns={columns}
        dataSource={filteredServers}
        pagination={{ pageSize: 10, hideOnSinglePage: true }}
        locale={{ emptyText: dataMode === 'demo' ? '演示模式未加载资产数据' : '暂无服务器资产' }}
        onRow={(server) => ({ onClick: () => navigate(`/assets/servers/${encodeURIComponent(server.id)}`), className: 'cursor-pointer' })}
      />
      <AddServerDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        onCreated={(server) => queryClient.setQueryData<AssetServer[]>(['asset-servers'], (current = []) => [server, ...current.filter((item) => item.id !== server.id)])}
      />
    </section>
  );
}
