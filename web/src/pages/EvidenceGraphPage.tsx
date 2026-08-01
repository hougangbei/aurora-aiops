import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Spin, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';

import { EvidenceGraph } from '../modules/aiops/components/EvidenceGraph';
import { getEvidence } from '../modules/aiops/api';
import { COLLAPSE_THRESHOLD, buildEvidenceLayout, type EvidenceFlowResult } from '../modules/aiops/evidenceLayout';
import { useAppStore } from '../stores/appStore';

const emptyFlow: EvidenceFlowResult = { nodes: [], edges: [], truncated: false, collapsed: {} };

export function EvidenceGraphPage() {
  const { id = '' } = useParams();
  const dataMode = useAppStore((state) => state.dataMode);
  const [showAll, setShowAll] = useState(false);
  const [flow, setFlow] = useState<EvidenceFlowResult>(emptyFlow);
  const [layouting, setLayouting] = useState(false);

  const evidenceQuery = useQuery({
    queryKey: ['aiops', 'evidence', id],
    queryFn: () => getEvidence(id),
    enabled: dataMode === 'live' && Boolean(id),
  });

  useEffect(() => {
    if (dataMode !== 'live' || !evidenceQuery.data) {
      setFlow(emptyFlow);
      return;
    }
    let cancelled = false;
    setLayouting(true);
    void buildEvidenceLayout(evidenceQuery.data.nodes, evidenceQuery.data.edges, { forceShowAll: showAll }).then((result) => {
      if (!cancelled) setFlow(result);
    }).finally(() => {
      if (!cancelled) setLayouting(false);
    });
    return () => {
      cancelled = true;
    };
  }, [dataMode, evidenceQuery.data, showAll]);

  return (
    <section className="space-y-4 rounded-[24px] border border-slate-200 bg-white p-6 shadow-[0_12px_36px_rgba(15,23,42,0.05)]">
      <div>
        <Typography.Title level={2} className="!mb-1">
          证据链视图
        </Typography.Title>
        <Typography.Text type="secondary">Incident {id} 的证据 DAG，节点按类型着色，边标注 relation。</Typography.Text>
      </div>

      {evidenceQuery.isError ? (
        <Alert type="error" showIcon message="加载证据失败" description={String(evidenceQuery.error)} />
      ) : null}

      {dataMode === 'demo' ? (
        <Alert type="info" showIcon message="演示模式不展示证据图。" />
      ) : evidenceQuery.isLoading ? (
        <div className="flex justify-center py-20">
          <Spin size="large" />
        </div>
      ) : (
        <>
          {flow.truncated && !showAll ? (
            <Alert
              type="warning"
              showIcon
              message={`证据节点超过 ${COLLAPSE_THRESHOLD} 个，已按类型折叠。`}
              action={
                <Button size="small" onClick={() => setShowAll(true)}>
                  显示全部
                </Button>
              }
            />
          ) : null}
          {layouting ? (
            <div className="flex justify-center py-10">
              <Spin tip="正在布局证据图..." />
            </div>
          ) : (
            <EvidenceGraph flow={flow} />
          )}
        </>
      )}
    </section>
  );
}
