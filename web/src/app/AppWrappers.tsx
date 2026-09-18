'use client';
import React, { ReactNode } from 'react';
import 'styles/index.css';

import dynamic from 'next/dynamic';

// 管理页大量直接读 document / window（暗色模式开关、侧栏状态），首版整体
// 关掉 SSR，避免服务端渲染阶段访问 DOM 报错。
const _NoSSR = ({ children }: { children: ReactNode }) => (
  <React.Fragment>{children}</React.Fragment>
);

const NoSSR = dynamic(() => Promise.resolve(_NoSSR), {
  ssr: false,
});

export default function AppWrappers({ children }: { children: ReactNode }) {
  return <NoSSR>{children}</NoSSR>;
}
