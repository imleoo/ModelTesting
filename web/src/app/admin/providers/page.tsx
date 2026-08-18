'use client';

import Card from 'components/card';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import {
  Model,
  Provider,
  createModel,
  createProvider,
  listModels,
  listProviders,
} from 'utils/apiClient';

// 对应设计方案 07 节 SOP 第 2 步"测试台登记供应商与模型"：供应商登记 +
// CAPABILITY_PROFILE 录入。测试用 API Key 不在这里填——12 节的首版简化是
// 完全不持久化 Key，只在「发起测试任务」页面按次临时提供（见
// /admin/test-runs/new）。
export default function ProvidersPage() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [loadError, setLoadError] = useState('');

  const [providerName, setProviderName] = useState('');
  const [providerContact, setProviderContact] = useState('');
  const [providerSubmitting, setProviderSubmitting] = useState(false);
  const [providerError, setProviderError] = useState('');

  const [modelProviderId, setModelProviderId] = useState('');
  const [modelKey, setModelKey] = useState('');
  const [endpoint, setEndpoint] = useState('');
  const [imageUrl, setImageUrl] = useState(false);
  const [videoUrl, setVideoUrl] = useState(false);
  const [toolCall, setToolCall] = useState(true);
  const [toolChoiceFunction, setToolChoiceFunction] = useState(false);
  const [thinkingMethods, setThinkingMethods] = useState(
    'enable_thinking',
  );
  const [defaultThinking, setDefaultThinking] = useState<
    'thinks_by_default' | 'no_thinking_by_default'
  >('thinks_by_default');
  const [reasoningEffort, setReasoningEffort] = useState(false);
  const [modelSubmitting, setModelSubmitting] = useState(false);
  const [modelError, setModelError] = useState('');

  async function reload() {
    try {
      const [ps, ms] = await Promise.all([listProviders(), listModels()]);
      setProviders(ps || []);
      setModels(ms || []);
      setLoadError('');
    } catch (e: any) {
      setLoadError(e.message || String(e));
    }
  }

  useEffect(() => {
    reload();
  }, []);

  async function onCreateProvider(e: React.FormEvent) {
    e.preventDefault();
    setProviderSubmitting(true);
    setProviderError('');
    try {
      await createProvider({ name: providerName, contact: providerContact });
      setProviderName('');
      setProviderContact('');
      await reload();
    } catch (e: any) {
      setProviderError(e.message || String(e));
    } finally {
      setProviderSubmitting(false);
    }
  }

  async function onCreateModel(e: React.FormEvent) {
    e.preventDefault();
    setModelSubmitting(true);
    setModelError('');
    try {
      const methods = thinkingMethods
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      await createModel({
        provider_id: modelProviderId,
        model_key: modelKey,
        endpoint_via_tokenpanel: endpoint,
        capability: {
          image_url: imageUrl,
          image_base64: true,
          video_url: videoUrl,
          video_base64: true,
          tool_call: toolCall,
          tool_choice_function: toolChoiceFunction,
          thinking_toggle_methods: methods,
          default_thinking_behavior: defaultThinking,
          reasoning_effort: reasoningEffort,
        },
      });
      setModelKey('');
      setEndpoint('');
      await reload();
    } catch (e: any) {
      setModelError(e.message || String(e));
    } finally {
      setModelSubmitting(false);
    }
  }

  const providerNameById = new Map(providers.map((p) => [p.id, p.name]));

  return (
    <div className="mt-5 grid grid-cols-1 gap-5 xl:grid-cols-2">
      {loadError && (
        <div className="col-span-full rounded-xl bg-red-100 p-4 text-sm text-red-700">
          加载失败：{loadError}
        </div>
      )}

      <Card extra="p-5">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          供应商登记
        </h2>
        <form onSubmit={onCreateProvider} className="mt-4 flex flex-col gap-3">
          <Field label="供应商名称">
            <TextInput
              value={providerName}
              onChange={setProviderName}
              required
              placeholder="如 MoonshotAI"
            />
          </Field>
          <Field label="联系方式（可选）">
            <TextInput
              value={providerContact}
              onChange={setProviderContact}
              placeholder="如 ops@example.com"
            />
          </Field>
          {providerError && (
            <p className="text-sm text-red-500">{providerError}</p>
          )}
          <SubmitButton submitting={providerSubmitting} label="创建供应商" />
        </form>

        <h3 className="mt-6 text-sm font-bold text-navy-700 dark:text-white">
          已登记供应商（{providers.length}）
        </h3>
        <ul className="mt-2 flex flex-col gap-1 text-sm">
          {providers.map((p) => (
            <li key={p.id} className="text-gray-600 dark:text-gray-300">
              {p.name}
              {p.contact ? ` · ${p.contact}` : ''}
            </li>
          ))}
          {providers.length === 0 && (
            <li className="text-gray-400">尚未登记供应商</li>
          )}
        </ul>
      </Card>

      <Card extra="p-5">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          模型登记（CAPABILITY_PROFILE）
        </h2>
        <form onSubmit={onCreateModel} className="mt-4 flex flex-col gap-3">
          <Field label="所属供应商">
            <select
              className={selectClassName}
              value={modelProviderId}
              onChange={(e) => setModelProviderId(e.target.value)}
              required
            >
              <option value="">请选择</option>
              {providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="model_key">
            <TextInput
              value={modelKey}
              onChange={setModelKey}
              required
              placeholder="如 kimi-k3（仅限字母/数字开头，其余为字母数字 . _ -）"
            />
          </Field>
          <Field label="tokenpanel 端点（endpoint_via_tokenpanel）">
            <TextInput
              value={endpoint}
              onChange={setEndpoint}
              required
              placeholder="如 https://jiwu.wtgo.com.cn/"
            />
          </Field>

          <div className="grid grid-cols-2 gap-2">
            <Checkbox label="image_url" checked={imageUrl} onChange={setImageUrl} />
            <Checkbox label="video_url" checked={videoUrl} onChange={setVideoUrl} />
            <Checkbox label="tool_call" checked={toolCall} onChange={setToolCall} />
            <Checkbox
              label="tool_choice_function"
              checked={toolChoiceFunction}
              onChange={setToolChoiceFunction}
            />
            <Checkbox
              label="reasoning_effort"
              checked={reasoningEffort}
              onChange={setReasoningEffort}
            />
          </div>
          <p className="text-xs text-gray-400">
            image_base64 / video_base64 按 PDF 最少支持要求固定必过，不可在此关闭。
          </p>

          <Field label="thinking_toggle_methods（逗号分隔，至少一项）">
            <TextInput
              value={thinkingMethods}
              onChange={setThinkingMethods}
              required
              placeholder="enable_thinking,thinking_type"
            />
          </Field>
          <Field label="default_thinking_behavior">
            <select
              className={selectClassName}
              value={defaultThinking}
              onChange={(e) => setDefaultThinking(e.target.value as any)}
            >
              <option value="thinks_by_default">thinks_by_default</option>
              <option value="no_thinking_by_default">
                no_thinking_by_default
              </option>
            </select>
          </Field>

          {modelError && <p className="text-sm text-red-500">{modelError}</p>}
          <SubmitButton submitting={modelSubmitting} label="创建模型" />
        </form>
      </Card>

      <Card extra="p-5 xl:col-span-2">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          已登记模型（{models.length}）
        </h2>
        <div className="mt-4 overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-gray-200 text-gray-500 dark:border-white/10 dark:text-gray-400">
                <th className="py-2 pr-4">供应商</th>
                <th className="py-2 pr-4">model_key</th>
                <th className="py-2 pr-4">端点</th>
                <th className="py-2 pr-4">操作</th>
              </tr>
            </thead>
            <tbody>
              {models.map((m) => (
                <tr
                  key={m.id}
                  className="border-b border-gray-100 dark:border-white/5"
                >
                  <td className="py-2 pr-4">
                    {providerNameById.get(m.provider_id) || m.provider_id}
                  </td>
                  <td className="py-2 pr-4">{m.model_key}</td>
                  <td className="py-2 pr-4 text-gray-500 dark:text-gray-400">
                    {m.endpoint_via_tokenpanel}
                  </td>
                  <td className="py-2 pr-4">
                    <Link
                      href={`/admin/test-runs/new?model_id=${m.id}`}
                      className="font-medium text-brand-500 hover:underline dark:text-brand-400"
                    >
                      发起测试任务
                    </Link>
                  </td>
                </tr>
              ))}
              {models.length === 0 && (
                <tr>
                  <td colSpan={4} className="py-4 text-gray-400">
                    尚未登记模型
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

function Checkbox({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex items-center gap-2 text-sm text-navy-700 dark:text-white">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
      />
      {label}
    </label>
  );
}

function SubmitButton({
  submitting,
  label,
}: {
  submitting: boolean;
  label: string;
}) {
  return (
    <button
      type="submit"
      disabled={submitting}
      className="mt-2 h-11 rounded-xl bg-brand-500 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-50"
    >
      {submitting ? '提交中…' : label}
    </button>
  );
}
