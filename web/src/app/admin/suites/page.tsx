'use client';

import Card from 'components/card';
import { useEffect, useState } from 'react';
import {
  SuiteSummary,
  cloneSuite,
  createSuite,
  listSuites,
} from 'utils/apiClient';

// 对应设计方案 10.2 节"套件克隆管理界面"——suites/ 目录下出现第二个供应商
// 套件（z-ai）后触发的引入条件。套件定义存在数据库 suites 表里（不再是
// SuitesRoot 目录下的文件，见 internal/api/config.go 的说明），这个页面
// 列出数据库现状，并提供「创建空白套件」「克隆已有套件」两个写操作入口——
// 具体用例定义（request_template/assertion_type/material_ref 等）仍然需要
// 人工对照已有套件的 SCHEMA.md 编辑，不是表单能安全生成的。
export default function SuitesPage() {
  const [suites, setSuites] = useState<SuiteSummary[]>([]);
  const [loadError, setLoadError] = useState('');

  const [createName, setCreateName] = useState('');
  const [createDescription, setCreateDescription] = useState('');
  const [createSubmitting, setCreateSubmitting] = useState(false);
  const [createError, setCreateError] = useState('');

  const [sourceSuiteId, setSourceSuiteId] = useState('');
  const [cloneName, setCloneName] = useState('');
  const [cloneSubmitting, setCloneSubmitting] = useState(false);
  const [cloneError, setCloneError] = useState('');

  async function reload() {
    try {
      const ss = await listSuites();
      setSuites(ss || []);
      setLoadError('');
    } catch (e: any) {
      setLoadError(e.message || String(e));
    }
  }

  useEffect(() => {
    reload();
  }, []);

  useEffect(() => {
    if (!sourceSuiteId && suites.length > 0) {
      setSourceSuiteId(suites[0].suite_id);
    }
  }, [suites, sourceSuiteId]);

  async function onCreateSuite(e: React.FormEvent) {
    e.preventDefault();
    setCreateSubmitting(true);
    setCreateError('');
    try {
      await createSuite({ name: createName, description: createDescription });
      setCreateName('');
      setCreateDescription('');
      await reload();
    } catch (e: any) {
      setCreateError(e.message || String(e));
    } finally {
      setCreateSubmitting(false);
    }
  }

  async function onCloneSuite(e: React.FormEvent) {
    e.preventDefault();
    setCloneSubmitting(true);
    setCloneError('');
    try {
      await cloneSuite({ source_suite_id: sourceSuiteId, new_name: cloneName });
      setCloneName('');
      await reload();
    } catch (e: any) {
      setCloneError(e.message || String(e));
    } finally {
      setCloneSubmitting(false);
    }
  }

  return (
    <div className="mt-5 grid grid-cols-1 gap-5 xl:grid-cols-2">
      {loadError && (
        <div className="col-span-full rounded-xl bg-red-100 p-4 text-sm text-red-700">
          加载失败：{loadError}
        </div>
      )}

      <Card extra="p-5">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          创建空白套件
        </h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          只生成一个结构合法、0 条用例的套件骨架。具体用例定义需要人工对照
          已有套件的 SCHEMA.md 手动编辑生成的 JSON 文件。
        </p>
        <form onSubmit={onCreateSuite} className="mt-4 flex flex-col gap-3">
          <Field label="套件名（仅限字母/数字/下划线/中划线）">
            <TextInput
              value={createName}
              onChange={setCreateName}
              required
              placeholder="如 acme-provider"
            />
          </Field>
          <Field label="描述（可选）">
            <TextInput
              value={createDescription}
              onChange={setCreateDescription}
              placeholder="这个套件的用途说明"
            />
          </Field>
          {createError && (
            <p className="text-sm text-red-500">{createError}</p>
          )}
          <SubmitButton submitting={createSubmitting} label="创建空白套件" />
        </form>
      </Card>

      <Card extra="p-5">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          克隆已有套件
        </h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          复制源套件的用例定义 + 素材文件到新目录，并自动改写内部
          引用（suite_id、素材清单路径、素材托管 URL），得到的新套件可以
          直接跑，再按新供应商的 CAPABILITY_PROFILE 差异增删用例。
        </p>
        <form onSubmit={onCloneSuite} className="mt-4 flex flex-col gap-3">
          <Field label="源套件">
            <select
              className={selectClassName}
              value={sourceSuiteId}
              onChange={(e) => setSourceSuiteId(e.target.value)}
              required
            >
              <option value="">请选择</option>
              {suites.map((s) => (
                <option key={s.suite_id} value={s.suite_id}>
                  {s.name}（{s.suite_version} · {s.case_count} 用例）
                </option>
              ))}
            </select>
          </Field>
          <Field label="新套件名（仅限字母/数字/下划线/中划线）">
            <TextInput
              value={cloneName}
              onChange={setCloneName}
              required
              placeholder="如 acme-provider"
            />
          </Field>
          {cloneError && <p className="text-sm text-red-500">{cloneError}</p>}
          <SubmitButton
            submitting={cloneSubmitting}
            label="克隆套件"
            disabled={!sourceSuiteId}
          />
        </form>
      </Card>

      <Card extra="p-5 xl:col-span-2">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          已有套件（{suites.length}）
        </h2>
        <div className="mt-4 overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-gray-200 text-gray-500 dark:border-white/10 dark:text-gray-400">
                <th className="py-2 pr-4">套件名</th>
                <th className="py-2 pr-4">版本</th>
                <th className="py-2 pr-4">用例数</th>
                <th className="py-2 pr-4">suite_id（发起任务时填这个）</th>
              </tr>
            </thead>
            <tbody>
              {suites.map((s) => (
                <tr
                  key={s.suite_id}
                  className="border-b border-gray-100 dark:border-white/5"
                >
                  <td className="py-2 pr-4">{s.name}</td>
                  <td className="py-2 pr-4 text-gray-500 dark:text-gray-400">
                    {s.suite_version}
                  </td>
                  <td className="py-2 pr-4">{s.case_count}</td>
                  <td className="py-2 pr-4 font-mono text-xs text-gray-500 dark:text-gray-400">
                    {s.suite_id}
                  </td>
                </tr>
              ))}
              {suites.length === 0 && (
                <tr>
                  <td colSpan={4} className="py-4 text-gray-400">
                    尚未登记套件
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}

const selectClassName =
  'mt-1 h-11 w-full rounded-xl border border-gray-200 bg-white/0 px-3 text-sm outline-none dark:!border-white/10 dark:text-white dark:bg-navy-800';

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col text-sm">
      <span className="font-medium text-navy-700 dark:text-white">
        {label}
      </span>
      {children}
    </label>
  );
}

function TextInput({
  value,
  onChange,
  placeholder,
  required,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  required?: boolean;
}) {
  return (
    <input
      className="mt-1 h-11 w-full rounded-xl border border-gray-200 bg-white/0 px-3 text-sm outline-none dark:!border-white/10 dark:text-white"
      value={value}
      required={required}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

function SubmitButton({
  submitting,
  label,
  disabled,
}: {
  submitting: boolean;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="submit"
      disabled={submitting || disabled}
      className="mt-2 h-11 rounded-xl bg-brand-500 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-50"
    >
      {submitting ? '提交中…' : label}
    </button>
  );
}
