'use client';

import Card from 'components/card';
import { useParams } from 'next/navigation';
import { testRunReportURL } from 'utils/apiClient';

// 对应设计方案 10.3 节「报告下载」页面：直接内嵌 report-cli/internal/api
// 生成的自包含单页 HTML（08 节报告结构），浏览器可以直接打印成 PDF——首版
// 不需要专门的 PDF 生成管线（10.1 节既定方案）。
export default function ReportPage() {
  const params = useParams<{ runId: string }>();
  const url = testRunReportURL(params.runId);

  return (
    <Card extra="mt-5 p-5">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-bold text-navy-700 dark:text-white">
          测试报告
        </h2>
        <a
          href={url}
          target="_blank"
          rel="noreferrer"
          className="rounded-xl bg-brand-500 px-4 py-2 text-sm font-medium text-white hover:bg-brand-600"
        >
          在新标签页打开 / 打印为 PDF
        </a>
      </div>
      <p className="mt-2 text-xs text-gray-400">
        报告尚未生成时（测试任务还在执行中）下方会显示 404，请返回测试任务
        页面等待任务完成。
      </p>
      <iframe
        src={url}
        className="mt-4 h-[80vh] w-full rounded-xl border border-gray-200 dark:border-white/10"
        title="测试报告"
      />
    </Card>
  );
}
