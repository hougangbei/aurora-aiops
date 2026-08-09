import { CheckCircleFilled, InfoCircleOutlined, RobotOutlined, WarningFilled } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Card, Descriptions, Space, Tag, Typography } from 'antd';

import { CoreSpinLoader } from '../components/ui/core-spin-loader';
import { getAIOpsReadiness } from '../modules/aiops/api';
import { useAppStore } from '../stores/appStore';

const environmentVariables = [
  ['AURORA_AIOPS_LLM_BASE_URL', 'OpenAI-compatible HTTPS 端点'],
  ['AURORA_AIOPS_LLM_API_KEY', '模型访问密钥，仅保存在后端运行环境'],
  ['AURORA_AIOPS_LLM_MODEL', '模型名称'],
  ['AURORA_AIOPS_LLM_API_STYLE', 'chat_completions 或 responses'],
] as const;

export function AIOpsSettingsPage() {
  const dataMode = useAppStore((state) => state.dataMode);
  const readinessQuery = useQuery({
    queryKey: ['aiops', 'readiness'],
    queryFn: getAIOpsReadiness,
    enabled: dataMode === 'live',
  });

  if (dataMode === 'live' && readinessQuery.isLoading) {
    return <CoreSpinLoader minHeight="320px" />;
  }

  const readiness = readinessQuery.data;
  const configured = dataMode === 'demo' || readiness?.modelConfigured === true;

  return (
    <section className="aurora-panel space-y-5 rounded-[24px] border p-6">
      <div>
        <Typography.Text className="aurora-eyebrow text-xs font-semibold uppercase tracking-[0.2em]">
          Runtime Capability
        </Typography.Text>
        <Typography.Title level={2} className="!mb-1 !mt-2">
          模型与运行能力
        </Typography.Title>
        <Typography.Text type="secondary">
          此页只展示后端真实运行状态，不加载密钥，也不会模拟保存配置。
        </Typography.Text>
      </div>

      {readinessQuery.isError ? (
        <Alert type="error" showIcon message="读取模型状态失败" description={String(readinessQuery.error)} />
      ) : null}

      <Card className="rounded-[20px]" title="当前状态">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <Space size={14}>
            <div className={`flex h-12 w-12 items-center justify-center rounded-2xl border ${configured ? 'border-emerald-300/25 bg-emerald-300/10 text-emerald-300' : 'border-amber-300/25 bg-amber-300/10 text-amber-300'}`}>
              <RobotOutlined className="text-xl" />
            </div>
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <Typography.Text strong className="text-lg">
                  {configured ? '模型已配置' : '模型未配置'}
                </Typography.Text>
                <Tag color={configured ? 'success' : 'warning'}>
                  {configured ? <><CheckCircleFilled /> 可用</> : <><WarningFilled /> 需配置</>}
                </Tag>
              </div>
              <Typography.Text type="secondary">
                {configured
                  ? readiness?.model || '演示模型'
                  : '分类和证据采集可用；根因、建议和风险评估暂停。'}
              </Typography.Text>
            </div>
          </Space>
        </div>

        <Descriptions
          className="!mt-6"
          size="small"
          column={{ xs: 1, md: 2 }}
          items={[
            { key: 'source', label: '配置来源', children: '后端环境变量' },
            { key: 'mutable', label: '页面内修改', children: <Tag>不支持</Tag> },
            { key: 'deterministic', label: '基础诊断', children: readiness?.deterministicRolesAvailable === false ? '不可用' : '可用' },
            { key: 'remediation', label: '修复护栏', children: readiness?.remediationAvailable === false ? '未接入' : '已启用' },
          ]}
        />
      </Card>

      <Card className="rounded-[20px]" title="配置方式">
        <Alert
          type="info"
          showIcon
          icon={<InfoCircleOutlined />}
          message="修改后端运行环境并重启服务后生效"
          description="为了避免凭证进入浏览器、localStorage 或前端日志，模型密钥不通过管理页面写入。"
        />
        <div className="mt-5 divide-y divide-indigo-200/10 rounded-[16px] border border-indigo-200/10 px-4">
          {environmentVariables.map(([name, description]) => (
            <div key={name} className="flex flex-col gap-1 py-4 sm:flex-row sm:items-center sm:justify-between sm:gap-6">
              <Typography.Text code copyable>{name}</Typography.Text>
              <Typography.Text type="secondary" className="text-sm">{description}</Typography.Text>
            </div>
          ))}
        </div>
        <Typography.Paragraph type="secondary" className="!mb-0 !mt-4 text-sm">
          API Key 永不通过此接口返回；能力状态接口只暴露“是否配置”和模型名称。
        </Typography.Paragraph>
      </Card>
    </section>
  );
}
