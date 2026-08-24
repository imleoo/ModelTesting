'use client';

import Card from 'components/card';
import { Fragment, useEffect, useState } from 'react';
import {
  CompareCaseCell,
  CompareCategory,
  CompareResult,
  CompareRunMeta,
  Model,
  Provider,
  TestRun,
  compareTestRuns,
  listModels,
  listProviders,
  listTestRuns,
} from 'utils/apiClient';

// 只读的多源对比查看页面：只支持"同一 model_key、不同供应商/端点"之间的
// 对比（跨 model_key 用例集不同，没法按 case_id 对齐，见后端
// internal/api/compare.go 的显式校验）。不改动现有发起测试流程，不产出任何
// 新数据，纯粹把多个已完成 TestRun 的历史结果拼到一张表里。
export default function ComparePage() {
  const [models, setModels] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loadError, setLoadError] = useState('');

  const [modelKey, setModelKey] = useState('');
  const [selectedModelIds, setSelectedModelIds] = useState<Set<string>>(
    new Set(),
  );
  const [runsByModelId, setRunsByModelId] = useState<
    Record<string, TestRun[]>
  >({});
  const [selectedRunByModelId, setSelectedRunByModelId] = useState<
    Record<string, string>
  >({});

  const [result, setResult] = useState<CompareResult | null>(null);
  const [compareError, setCompareError] = useState('');
  const [comparing, setComparing] = useState(false);

  useEffect(() => {
    Promise.all([listModels(), listProviders()])
      .then(([ms, ps]) => {
        setModels(ms);
        setProviders(ps);
      })
      .catch((e) => setLoadError(e.message || String(e)));
  }, []);

  const providerNameById = new Map(providers.map((p) => [p.id, p.name]));
  function endpointLabel(m: Model): string {
    const providerName = providerNameById.get(m.provider_id);
    return providerName
      ? `${providerName} · ${m.endpoint_via_tokenpanel}`
      : m.endpoint_via_tokenpanel;
  }

  // 只有同一 model_key 下登记了 ≥2 个端点，才有对比的意义——少于 2 个的
  // model_key 直接从下拉框里过滤掉，不需要后端配合。
  const modelsByKey = new Map<string, Model[]>();
  for (const m of models) {
    const arr = modelsByKey.get(m.model_key) || [];
    arr.push(m);
    modelsByKey.set(m.model_key, arr);
  }
  const comparableModelKeys = [...modelsByKey.keys()].filter(
    (k) => (modelsByKey.get(k)?.length || 0) >= 2,
  );
  const candidateModels = modelKey ? modelsByKey.get(modelKey) || [] : [];

  function onModelKeyChange(key: string) {
    setModelKey(key);
    setSelectedModelIds(new Set());
    setSelectedRunByModelId({});
    setResult(null);
    setCompareError('');
  }

  function toggleModel(m: Model) {
    const isSelected = selectedModelIds.has(m.id);
    const next = new Set(selectedModelIds);
    if (isSelected) {
      next.delete(m.id);
      setSelectedRunByModelId((prev) => {
        const nextRuns = { ...prev };
        delete nextRuns[m.id];
        return nextRuns;
      });
    } else {
      next.add(m.id);
      if (!runsByModelId[m.id]) {
        listTestRuns(m.id)
          .then((runs) =>
            setRunsByModelId((prev) => ({ ...prev, [m.id]: runs })),
          )
          .catch((e) => setLoadError(e.message || String(e)));
      }
    }
    setSelectedModelIds(next);
    setResult(null);
    setCompareError('');
  }

  const chosenRunIds = Object.values(selectedRunByModelId).filter(Boolean);

  async function onCompare() {
    setComparing(true);
    setCompareError('');
    try {
      const r = await compareTestRuns(chosenRunIds);
      setResult(r);
    } catch (e: any) {
      setResult(null);
      setCompareError(e.message || String(e));
    } finally {
      setComparing(false);
    }
  }

  return (
    <div className="mt-5 flex flex-col gap-5">
      <Card extra="p-5">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          结果对比
        </h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          挑选同一个 model_key 下、不同供应商/端点各自已经跑完的一次历史
          TestRun，按 case_id 并排展示结果——不发起新测试，只读已产出的数据。
        </p>
        {loadError && (
          <p className="mt-2 text-sm text-red-500">加载失败：{loadError}</p>
        )}

        <label className="mt-4 flex flex-col text-sm">
          <span className="font-medium text-navy-700 dark:text-white">
            model_key
          </span>
          <select
            className="mt-1 h-11 max-w-md rounded-xl border border-gray-200 bg-white/0 px-3 text-sm outline-none dark:!border-white/10 dark:bg-navy-800 dark:text-white"
            value={modelKey}
            onChange={(e) => onModelKeyChange(e.target.value)}
          >
            <option value="">请选择</option>
            {comparableModelKeys.map((k) => (
              <option key={k} value={k}>
                {k}（{modelsByKey.get(k)?.length} 个端点）
              </option>
            ))}
          </select>
          {models.length > 0 && comparableModelKeys.length === 0 && (
            <span className="mt-1 text-xs text-gray-400">
              还没有任何 model_key 登记了 2 个及以上的端点，暂时无法对比。
            </span>
          )}
        </label>

        {modelKey && (
          <div className="mt-4 flex flex-col gap-3">
            {candidateModels.map((m) => {
              const isSelected = selectedModelIds.has(m.id);
              const runs = (runsByModelId[m.id] || []).filter(
                (r) => !!r.case_results_path,
              );
              return (
                <div
                  key={m.id}
                  className="rounded-xl border border-gray-200 p-3 dark:border-white/10"
                >
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleModel(m)}
                    />
                    <span className="font-medium text-navy-700 dark:text-white">
                      {endpointLabel(m)}
                    </span>
                  </label>
                  {isSelected && (
                    <div className="mt-2 pl-6">
                      <select
                        className="h-9 w-full max-w-md rounded-lg border border-gray-200 bg-white/0 px-2 text-sm outline-none dark:!border-white/10 dark:bg-navy-800 dark:text-white"
                        value={selectedRunByModelId[m.id] || ''}
                        onChange={(e) =>
                          setSelectedRunByModelId((prev) => ({
                            ...prev,
                            [m.id]: e.target.value,
                          }))
                        }
                      >
                        <option value="">请选择一次历史运行</option>
                        {runs.map((r) => (
                          <option key={r.id} value={r.id}>
                            {r.started_at} · {r.status}
                          </option>
                        ))}
                      </select>
                      {runs.length === 0 && (
                        <span className="mt-1 block text-xs text-gray-400">
                          该端点暂无已产出结果的历史运行。
                        </span>
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

        {compareError && (
          <p className="mt-3 text-sm text-red-500">{compareError}</p>
        )}
        <button
          type="button"
          disabled={chosenRunIds.length < 2 || comparing}
          onClick={onCompare}
          className="mt-4 h-11 rounded-xl bg-brand-500 px-4 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-50"
        >
          {comparing
            ? '对比中…'
            : `生成对比${chosenRunIds.length >= 2 ? `（${chosenRunIds.length} 个端点）` : ''}`}
        </button>
      </Card>

      {result && <CompareResultView result={result} />}
    </div>
  );
}

const cellStatusColor: Record<string, string> = {
  PASS: 'bg-green-100 text-green-700',
  FAIL: 'bg-red-100 text-red-700',
  MANUAL_REVIEW: 'bg-yellow-100 text-yellow-700',
  NOT_DECLARED: 'bg-gray-200 text-gray-700',
  MISSING: 'bg-gray-100 text-gray-400',
};

// splitByBase22 把「按 category 分组」的 categories 拆成 22 项基础用例 /
// 附加能力用例两组，组内保持原有的 category 分组结构，对齐 RunDetail 现有的
// 「22 项基础用例表 + 附加能力用例表」两张表结构。
function splitByBase22(categories: CompareCategory[]): {
  base22: CompareCategory[];
  additional: CompareCategory[];
} {
  const base22: CompareCategory[] = [];
  const additional: CompareCategory[] = [];
  for (const cat of categories) {
    const base22Cases = cat.cases.filter((c) => c.counts_in_base22);
    const additionalCases = cat.cases.filter((c) => !c.counts_in_base22);
    if (base22Cases.length > 0) {
      base22.push({ category: cat.category, cases: base22Cases });
    }
    if (additionalCases.length > 0) {
      additional.push({ category: cat.category, cases: additionalCases });
    }
  }
  return { base22, additional };
}

function base22PassCount(
  base22: CompareCategory[],
  runId: string,
): { pass: number; total: number } {
  let pass = 0;
  let total = 0;
  for (const cat of base22) {
    for (const row of cat.cases) {
      total += 1;
      if (row.cells[runId]?.status === 'PASS') pass += 1;
    }
  }
  return { pass, total };
}

function CompareResultView({ result }: { result: CompareResult }) {
  const { base22, additional } = splitByBase22(result.categories);

  return (
    <Card extra="p-5">
      <h3 className="text-md font-bold text-navy-700 dark:text-white">
        {result.model_key} · {result.suite_id}
      </h3>

      <div className="mt-3 flex flex-wrap gap-3">
        {result.runs.map((r) => {
          const { pass, total } = base22PassCount(base22, r.run_id);
          return (
            <div
              key={r.run_id}
              className="min-w-[220px] rounded-xl border border-gray-200 p-3 text-xs dark:border-white/10"
            >
              <div className="font-semibold text-navy-700 dark:text-white">
                {r.provider_name}
              </div>
              <div className="mt-0.5 break-all text-gray-400">
                {r.endpoint_via_tokenpanel}
              </div>
              <div className="mt-1 text-gray-500 dark:text-gray-300">
                开始：{r.started_at}
              </div>
              {r.finished_at && (
                <div className="text-gray-500 dark:text-gray-300">
                  结束：{r.finished_at}
                </div>
              )}
              <div className="mt-1 font-medium text-navy-700 dark:text-white">
                22 项基础用例：{pass}/{total} 通过
              </div>
            </div>
          );
        })}
      </div>

      <h4 className="mt-6 text-sm font-bold text-navy-700 dark:text-white">
        22 项基础用例
      </h4>
      <div className="mt-3 overflow-x-auto">
        <CompareCaseTable categories={base22} runs={result.runs} />
      </div>

      {additional.length > 0 && (
        <>
          <h4 className="mt-6 text-sm font-bold text-navy-700 dark:text-white">
            附加能力用例（不计入 22 项分母）
          </h4>
          <div className="mt-3 overflow-x-auto">
            <CompareCaseTable categories={additional} runs={result.runs} />
          </div>
        </>
      )}
    </Card>
  );
}

function CompareCaseTable({
  categories,
  runs,
}: {
  categories: CompareCategory[];
  runs: CompareRunMeta[];
}) {
  return (
    <table className="w-full text-left text-sm">
      <thead>
        <tr className="border-b border-gray-200 text-gray-500 dark:border-white/10 dark:text-gray-400">
          <th className="py-2 pr-4">用例</th>
          {runs.map((r) => (
            <th key={r.run_id} className="py-2 pr-4">
              {r.provider_name}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {categories.map((cat) => (
          <Fragment key={cat.category}>
            <tr className="bg-gray-50 dark:bg-navy-900">
              <td
                colSpan={runs.length + 1}
                className="py-1.5 pr-4 text-xs font-semibold uppercase tracking-wide text-gray-400"
              >
                {cat.category}
              </td>
            </tr>
            {cat.cases.map((row) => (
              <tr
                key={row.case_id}
                className="border-b border-gray-100 dark:border-white/5"
              >
                <td className="py-2 pr-4 align-top">
                  <div>{row.case_id}</div>
                  <div className="text-xs text-gray-400">{row.name}</div>
                </td>
                {runs.map((r) => (
                  <td key={r.run_id} className="py-2 pr-4 align-top">
                    <CompareCell cell={row.cells[r.run_id]} />
                  </td>
                ))}
              </tr>
            ))}
          </Fragment>
        ))}
        {categories.length === 0 && (
          <tr>
            <td colSpan={runs.length + 1} className="py-4 text-gray-400">
              无
            </td>
          </tr>
        )}
      </tbody>
    </table>
  );
}

function CompareCell({ cell }: { cell?: CompareCaseCell }) {
  if (!cell) {
    return <span className="text-gray-400">—</span>;
  }
  return (
    <div title={cell.fail_reason || undefined}>
      <span
        className={`rounded-full px-2 py-0.5 text-xs font-medium ${
          cellStatusColor[cell.status] || 'bg-gray-200 text-gray-700'
        }`}
      >
        {cell.status}
      </span>
      {cell.attempts > 0 && (
        <div className="mt-1 text-xs text-gray-400">
          {cell.passed_attempts}/{cell.attempts} ·{' '}
          {Math.round(cell.avg_latency_ms)}ms
        </div>
      )}
    </div>
  );
}
