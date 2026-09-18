import type { Metadata, Viewport } from 'next';
import localFont from 'next/font/local';
import React, { ReactNode } from 'react';
import AppWrappers from './AppWrappers';

// DM Sans 由 next/font/local 自托管（构建期打进 .next/static），不再依赖
// Google Fonts 外链——内网/国内环境访问不到 fonts.googleapis.com，外链
// 只会静默失败。中文字形由 tailwind.config.js 里的系统字体栈兜底。
const dmSans = localFont({
  variable: '--font-dm-sans',
  display: 'swap',
  src: [
    { path: '../fonts/dm-sans/DMSans-Regular.ttf', weight: '400', style: 'normal' },
    { path: '../fonts/dm-sans/DMSans-Italic.ttf', weight: '400', style: 'italic' },
    { path: '../fonts/dm-sans/DMSans-Medium.ttf', weight: '500', style: 'normal' },
    { path: '../fonts/dm-sans/DMSans-MediumItalic.ttf', weight: '500', style: 'italic' },
    { path: '../fonts/dm-sans/DMSans-Bold.ttf', weight: '700', style: 'normal' },
    { path: '../fonts/dm-sans/DMSans-BoldItalic.ttf', weight: '700', style: 'italic' },
  ],
});

export const metadata: Metadata = {
  title: {
    default: '模型自测台',
    template: '%s · 模型自测台',
  },
  description:
    '面向多供应商 LLM 接入场景的自测执行与报告平台：功能用例断言、压测与可归档报告。',
};

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  themeColor: '#4318FF',
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh-CN" className={dmSans.variable}>
      <body id={'root'}>
        <AppWrappers>{children}</AppWrappers>
      </body>
    </html>
  );
}
