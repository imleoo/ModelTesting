'use client';

import Card from 'components/card';
import Link from 'next/link';
import { useParams, useRouter, useSearchParams } from 'next/navigation';
import { useEffect, useState } from 'react';
import {
  ApiError,
  CaseResult,
  Model,
  Provider,
  TestRun,
  getTestRun,
  getTestRunCaseResults,
  isTerminalStatus,
  launchTestRun,
  listModels,
  listProviders,
} from 'utils/apiClient';

// 合并了设计方案 10.3 节「发起任务」与「结果详情」两个概念页面：
// /admin/test-runs/new 是发起表单，/admin/test-runs/<真实 id> 是结果详情
// （轮询状态直到终态，展示 22 项用例结果表）。
export default function TestRunPage() {
  const params = useParams<{ id: string }>();
  if (params.id === 'new') {
    return <LaunchForm />;
  }
  return <RunDetail runId={params.id} />;
}

const statusLabel: Record<string, string> = {
  PENDING: '排队中',
  RUNNING_FUNCTIONAL: '功能测试执行中',
  FUNCTIONAL_BLOCKED: '功能测试未全部通过，压测已跳过',
  RUNNING_BENCHMARK: '压测执行中',
  COMPLETED: '已完成',
  FAILED: '执行出错',
};

const statusColor: Record<string, string> = {
  PENDING: 'bg-gray-200 text-gray-700',
  RUNNING_FUNCTIONAL: 'bg-blue-100 text-blue-700',
  FUNCTIONAL_BLOCKED: 'bg-yellow-100 text-yellow-700',
  RUNNING_BENCHMARK: 'bg-blue-100 text-blue-700',
  COMPLETED: 'bg-green-100 text-green-700',
  FAILED: 'bg-red-100 text-red-700',
};

function LaunchForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [models, setModels] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [modelId, setModelId] = useState(searchParams.get('model_id') || '');
  const [suiteId, setSuiteId] = useState('kimi-k3/suite.v1.json');
  const [apiKey, setApiKey] = useState('');
  const [totalSessions, setTotalSessions] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    Promise.all([listModels(), listProviders()])
      .then(([ms, ps]) => {
        setModels(ms);
        setProviders(ps);
      })
      .catch((e) => setError(e.message || String(e)));
  }, []);

  // 同一个 model_key（如上游真实模型名 "kimi-k3"）可能挂在多个供应商/通道
  // 下面，只显示 model_key 会分不清选的是哪一条，拼上供应商名字消歧义。
  const providerNameById = new Map(providers.map((p) => [p.id, p.name]));
  function modelLabel(m: Model): string {
    const providerName = providerNameById.get(m.provider_id);
    return providerName ? `${providerName} · ${m.model_key}` : m.model_key;
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      const run = await launchTestRun({
        model_id: modelId,
        suite_id: suiteId,
        api_key: apiKey || undefined,
        total_sessions: totalSessions ? Number(totalSessions) : undefined,
      });
      router.push(`/admin/test-runs/${run.id}`);
    } catch (e: any) {
      setError(e.message || String(e));
      setSubmitting(false);
    }
  }

  return (
    <Card extra="mt-5 max-w-xl p-5">
      <h2 className="text-lg font-bold text-navy-700 dark:text-white">
        发起测试任务
      </h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
        对应设计方案 09 节执行时序：先跑全部功能用例，22 项基础用例 + 已声明
        附加能力用例全部通过且无未清空 MANUAL_REVIEW 才会自动解锁压测。
      </p>
      <form onSubmit={onSubmit} className="mt-4 flex flex-col gap-3">
        <label className="flex flex-col text-sm">
          <span className="font-medium text-navy-700 dark:text-white">
            被测模型
          </span>
          <select
            className="mt-1 h-11 rounded-xl border border-gray-200 bg-white/0 px-3 text-sm outline-none dark:!border-white/10 dark:bg-navy-800 dark:text-white"
            value={modelId}
            onChange={(e) => setModelId(e.target.value)}
            required
          >
            <option value="">请选择</option>
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {modelLabel(m)}
              </option>
            ))}
          </select>
          {models.length === 0 && (
            <span className="mt-1 text-xs text-gray-400">
              还没有已登记的模型，请先前往
              <Link href="/admin/providers" className="text-brand-500">
                模型登记
              </Link>
              页面创建。
            </span>
          )}
        </label>

        <label className="flex flex-col text-sm">
          <span className="font-medium text-navy-700 dark:text-white">
            suite_id（相对套件根目录的路径）
          </span>
          <input
            className="mt-1 h-11 rounded-xl border border-gray-200 bg-white/0 px-3 text-sm outline-none dark:!border-white/10 dark:text-white"
            value={suiteId}
            onChange={(e) => setSuiteId(e.target.value)}
            required
          />
        </label>

        <label className="flex flex-col text-sm">
          <span className="font-medium text-navy-700 dark:text-white">
            API Key（仅本次任务使用，不会被持久化保存）
          </span>
          <input
            type="password"
            className="mt-1 h-11 rounded-xl border border-gray-200 bg-white/0 px-3 text-sm outline-none dark:!border-white/10 dark:text-white"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="测试用 API Key"
          />
        </label>

        <label className="flex flex-col text-sm">
          <span className="font-medium text-navy-700 dark:text-white">
            压测规模 total_sessions（可选，留空用服务端默认值）
          </span>
          <input
            type="number"
            min={1}
            className="mt-1 h-11 rounded-xl border border-gray-200 bg-white/0 px-3 text-sm outline-none dark:!border-white/10 dark:text-white"
            value={totalSessions}
            onChange={(e) => setTotalSessions(e.target.value)}
          />
        </label>

        {error && <p className="text-sm text-red-500">{error}</p>}
        <button
          type="submit"
          disabled={submitting || !modelId}
          className="mt-2 h-11 rounded-xl bg-brand-500 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-50"
        >
          {submitting ? '发起中…' : '发起测试任务'}
        </button>
      </form>
    </Card>
  );
}

function RunDetail({ runId }: { runId: string }) {
  const [run, setRun] = useState<TestRun | null>(null);
  const [caseResults, setCaseResults] = useState<CaseResult[] | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      try {
        const r = await getTestRun(runId);
        if (cancelled) return;
        setRun(r);
        setError('');
        if (r.case_results_path && !caseResults) {
          getTestRunCaseResults(runId)
            .then((cr) => !cancelled && setCaseResults(cr))
            .catch(() => {
              /* 结果文件可能还在写入过程中，下一轮轮询再试 */
            });
        }
        if (!isTerminalStatus(r.status)) {
          timer = setTimeout(poll, 2000);
        }
      } catch (e: any) {
        if (cancelled) return;
        setError(e.message || String(e));
        // 404（TestRun 根本不存在）是永久性错误，重试没有意义、只会一直
        // 显示同一个错误；网络抖动/5xx 这类临时性错误才值得继续按原节奏
        // 重试——只要任务本身还没到终态，下一轮成功时 setError('') 会
        // 自动清空这条错误提示。
        if (!(e instanceof ApiError && e.status === 404)) {
          timer = setTimeout(poll, 2000);
        }
      }
    }
    poll();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runId]);

  // 只有从未成功加载过任何数据时才整页显示错误——一旦轮询期间发生的是
  // 临时性网络错误，已经取到的 run/caseResults 应该继续展示，不能因为
  // 某一轮轮询失败就把已经渲染出来的结果表整个隐藏掉。
  if (!run && error) {
    return (
      <Card extra="mt-5 p-5">
        <p className="text-sm text-red-500">加载失败：{error}</p>
      </Card>
    );
  }
  if (!run) {
    return (
      <Card extra="mt-5 p-5">
        <p className="text-sm text-gray-500">加载中…</p>
      </Card>
    );
  }

  const base22 = (caseResults || []).filter((c) => c.counts_in_base22);
  const additional = (caseResults || []).filter((c) => !c.counts_in_base22);
  const base22Pass = base22.filter((c) => c.status === 'PASS').length;

  return (
    <div className="mt-5 flex flex-col gap-5">
      {error && (
        <div className="rounded-xl bg-yellow-100 p-3 text-sm text-yellow-700">
          轮询遇到网络问题，正在自动重试：{error}
        </div>
      )}
      <Card extra="p-5">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-bold text-navy-700 dark:text-white">
            测试任务 {run.id}
          </h2>
          <span
            className={`rounded-full px-3 py-1 text-xs font-medium ${
              statusColor[run.status] || 'bg-gray-200 text-gray-700'
            }`}
          >
            {statusLabel[run.status] || run.status}
          </span>
        </div>
        <dl className="mt-3 grid grid-cols-2 gap-2 text-sm text-gray-600 dark:text-gray-300">
          <dt>套件</dt>
          <dd>{run.suite_id}</dd>
          <dt>开始时间</dt>
          <dd>{run.started_at}</dd>
          {run.finished_at && (
            <>
              <dt>结束时间</dt>
              <dd>{run.finished_at}</dd>
            </>
          )}
          {run.error_message && (
            <>
              <dt>错误信息</dt>
              <dd className="text-red-500">{run.error_message}</dd>
            </>
          )}
        </dl>
        {isTerminalStatus(run.status) && (
          <Link
            href={`/admin/reports/${run.id}`}
            className="mt-4 inline-block rounded-xl bg-brand-500 px-4 py-2 text-sm font-medium text-white hover:bg-brand-600"
          >
            查看报告
          </Link>
        )}
      </Card>

      {caseResults && (
        <Card extra="p-5">
          <h3 className="text-md font-bold text-navy-700 dark:text-white">
            22 项基础用例：{base22Pass} / {base22.length} 通过
          </h3>
          <div className="mt-3 overflow-x-auto">
            <CaseResultTable rows={base22} />
          </div>
          {additional.length > 0 && (
            <>
              <h3 className="mt-6 text-md font-bold text-navy-700 dark:text-white">
                附加能力用例（不计入 22 项分母）
              </h3>
              <div className="mt-3 overflow-x-auto">
                <CaseResultTable rows={additional} />
              </div>
            </>
          )}
        </Card>
      )}
    </div>
  );
}

function CaseResultTable({ rows }: { rows: CaseResult[] }) {
  return (
    <table className="w-full text-left text-sm">
      <thead>
        <tr className="border-b border-gray-200 text-gray-500 dark:border-white/10 dark:text-gray-400">
          <th className="py-2 pr-4">用例 ID</th>
          <th className="py-2 pr-4">状态</th>
          <th className="py-2 pr-4">通过次数</th>
          <th className="py-2 pr-4">失败原因</th>
          <th className="py-2 pr-4">请求/响应明细</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.id} className="border-b border-gray-100 dark:border-white/5">
            <td className="py-2 pr-4 align-top">{r.case_id}</td>
            <td className="py-2 pr-4 align-top">
              <span
                className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                  r.status === 'PASS'
                    ? 'bg-green-100 text-green-700'
                    : r.status === 'FAIL'
                      ? 'bg-red-100 text-red-700'
                      : r.status === 'MANUAL_REVIEW'
                        ? 'bg-yellow-100 text-yellow-700'
                        : 'bg-gray-200 text-gray-700'
                }`}
              >
                {r.status}
              </span>
            </td>
            <td className="py-2 pr-4 align-top">
              {r.passed_attempts}/{r.attempts}
            </td>
            <td className="py-2 pr-4 align-top text-gray-500 dark:text-gray-400">
              {r.fail_reason}
            </td>
            <td className="py-2 pr-4 align-top">
              <CaseAttemptsDetail attempts={r.case_attempts} />
            </td>
          </tr>
        ))}
        {rows.length === 0 && (
          <tr>
            <td colSpan={5} className="py-4 text-gray-400">
              无
            </td>
          </tr>
        )}
      </tbody>
    </table>
  );
}

// CaseAttemptsDetail 对应设计方案 10.3 节要求："失败请求/响应体查看器用
// Chakra Modal/Popover 承载"——本项目实际可用的 Chakra 依赖只有零散几个
// 包（没有完整的 @chakra-ui/react 组件库，见 apiClient.ts 顶部注释同类
// 说明），这里用原生 <details>/<summary> 达到同样的"默认折叠、按需展开"
// 效果，不需要额外引入 Modal/Popover 依赖，和 P4 报告生成阶段 HTML 模板
// 里请求/响应明细的呈现方式保持一致（internal/report/template.go）。
function CaseAttemptsDetail({
  attempts,
}: {
  attempts: CaseResult['case_attempts'];
}) {
  if (!attempts || attempts.length === 0) {
    return <span className="text-gray-400">—</span>;
  }
  return (
    <details>
      <summary className="cursor-pointer text-brand-500 hover:underline dark:text-brand-400">
        {attempts.length} 次请求明细
      </summary>
      <div className="mt-2 flex flex-col gap-3">
        {attempts.map((a) => (
          <div
            key={a.id}
            className="rounded-lg border border-gray-200 p-2 text-xs dark:border-white/10"
          >
            <p className="text-gray-500 dark:text-gray-400">
              第 {a.attempt_index} 次（{a.variant_label}）· HTTP{' '}
              {a.http_status} · {a.latency_ms}ms ·{' '}
              {a.passed ? (
                <span className="text-green-600">✓ 通过</span>
              ) : (
                <span className="text-red-500">✗ 未通过：{a.fail_reason}</span>
              )}
            </p>
            <pre className="mt-1 max-h-60 overflow-auto whitespace-pre-wrap rounded bg-gray-50 p-2 dark:bg-navy-900">
              请求：{a.request_body}
            </pre>
            <pre className="mt-1 max-h-60 overflow-auto whitespace-pre-wrap rounded bg-gray-50 p-2 dark:bg-navy-900">
              响应：{a.response_body}
            </pre>
          </div>
        ))}
      </div>
    </details>
  );
}
