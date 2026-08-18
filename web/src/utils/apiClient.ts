// apiClient 封装对 internal/api（Gin API 服务）的调用。
//
// 已知限制：首版不处理 API 服务的 -auth-token 鉴权——浏览器直接发起
// fetch，不携带任何 Authorization 头。这与 api-server 默认（未配置
// -auth-token）不鉴权的首版部署方式（本地/内网）保持一致；如果后续给
// api-server 配置了固定 Token，这个前端需要相应升级成服务端代理转发
// （避免把 Token 直接下发到浏览器），属于后续按需引入项，这里不做。

export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL || 'http://localhost:8090';

export type Provider = {
  id: string;
  name: string;
  contact?: string;
};

export type CapabilityProfile = {
  model_id?: string;
  image_url: boolean;
  image_base64: boolean;
  video_url: boolean;
  video_base64: boolean;
  tool_call: boolean;
  tool_choice_function: boolean;
  thinking_toggle_methods: string[];
  default_thinking_behavior: 'thinks_by_default' | 'no_thinking_by_default';
  reasoning_effort: boolean;
};

export type Model = {
  id: string;
  provider_id: string;
  model_key: string;
  endpoint_via_tokenpanel: string;
  capability: CapabilityProfile;
};

export type TestRunStatus =
  | 'PENDING'
  | 'RUNNING_FUNCTIONAL'
  | 'FUNCTIONAL_BLOCKED'
  | 'RUNNING_BENCHMARK'
  | 'COMPLETED'
  | 'FAILED';

export type TestRun = {
  id: string;
  model_id: string;
  suite_id: string;
  status: TestRunStatus;
  started_at: string;
  finished_at?: string;
  case_results_path?: string;
  benchmark_result_path?: string;
  error_message?: string;
};

export type CaseAttempt = {
  id: string;
  case_result_id: string;
  attempt_index: number;
  variant_label: string;
  http_status: number;
  latency_ms: number;
  request_body: string;
  response_body: string;
  reasoning_tokens?: number;
  passed: boolean;
  fail_reason?: string;
};

export type CaseResult = {
  id: string;
  case_id: string;
  status: 'PASS' | 'FAIL' | 'NOT_DECLARED' | 'MANUAL_REVIEW';
  attempts: number;
  passed_attempts: number;
  pass_rate: number;
  fail_reason?: string;
  case_attempts: CaseAttempt[];
  counts_in_base22: boolean;
};

// ApiError 携带 HTTP 状态码，供调用方区分"永久性错误"（如 404，重试没有
// 意义）和"临时性错误"（网络抖动/5xx，值得重试）——普通 Error 丢了这个
// 信息，会让轮询逻辑只能对所有错误一视同仁地无限重试或者一律放弃。
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) },
    cache: 'no-store',
  });
  if (!res.ok) {
    let message = `请求失败：HTTP ${res.status}`;
    try {
      const body = await res.json();
      if (body?.error) message = body.error;
    } catch {
      // 响应体不是 JSON（比如 404 页面），保留默认错误信息即可。
    }
    throw new ApiError(res.status, message);
  }
  return res.json() as Promise<T>;
}

export function listProviders(): Promise<Provider[]> {
  return request<Provider[]>('/api/providers');
}

export function createProvider(input: {
  name: string;
  contact?: string;
}): Promise<Provider> {
  return request<Provider>('/api/providers', {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

export function listModels(): Promise<Model[]> {
  return request<Model[]>('/api/models');
}

export function getModel(id: string): Promise<Model> {
  return request<Model>(`/api/models/${encodeURIComponent(id)}`);
}

export function createModel(input: {
  provider_id: string;
  model_key: string;
  endpoint_via_tokenpanel: string;
  capability: CapabilityProfile;
}): Promise<Model> {
  return request<Model>('/api/models', {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

export function launchTestRun(input: {
  model_id: string;
  suite_id: string;
  api_key?: string;
  total_sessions?: number;
}): Promise<TestRun> {
  return request<TestRun>('/api/test-runs', {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

export function getTestRun(id: string): Promise<TestRun> {
  return request<TestRun>(`/api/test-runs/${encodeURIComponent(id)}`);
}

export function listTestRuns(modelId: string): Promise<TestRun[]> {
  return request<TestRun[]>(
    `/api/test-runs?model_id=${encodeURIComponent(modelId)}`,
  );
}

export function getTestRunCaseResults(id: string): Promise<CaseResult[]> {
  return request<CaseResult[]>(
    `/api/test-runs/${encodeURIComponent(id)}/case-results`,
  );
}

export function testRunReportURL(id: string): string {
  return `${API_BASE_URL}/api/test-runs/${encodeURIComponent(id)}/report`;
}

// 终态：到了这几个状态后前端可以停止轮询。
export function isTerminalStatus(status: TestRunStatus): boolean {
  return (
    status === 'COMPLETED' ||
    status === 'FUNCTIONAL_BLOCKED' ||
    status === 'FAILED'
  );
}
