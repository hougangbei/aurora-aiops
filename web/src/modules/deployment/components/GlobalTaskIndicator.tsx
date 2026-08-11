import { CloudSyncOutlined } from '@ant-design/icons';
import { Badge, Button } from 'antd';
import { Link } from 'react-router-dom';

import type { DeploymentTask } from '../types';

export function GlobalTaskIndicator({ tasks }: { tasks: DeploymentTask[] }) {
  const task = tasks.find((item) => item.status === 'queued' || item.status === 'running');
  if (!task) return null;
  return (
    <Badge count={`${task.percent}%`} offset={[-4, 4]}>
      <Button type="text" icon={<CloudSyncOutlined />}>
        <Link aria-label={`任务进度 ${task.percent}%`} to={`/assets/servers/${encodeURIComponent(task.serverId)}?task=${encodeURIComponent(task.id)}`}>任务进度</Link>
      </Button>
    </Badge>
  );
}

