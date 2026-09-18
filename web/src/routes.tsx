import React from 'react';

// Icon Imports
import {
  MdCompareArrows,
  MdLibraryBooks,
  MdPeopleAlt,
  MdPlayCircleOutline,
} from 'react-icons/md';

import { IRoute } from 'types/navigation';

// 对应设计方案 10.3 节路由映射：模型登记（/admin/providers）、发起任务+
// 结果详情合并（/admin/test-runs/[id]）。报告下载（/admin/reports/[runId]）
// 从测试任务结果页跳转进入，不单独放进侧边栏一级导航——首版没有历史报告
// 列表页（10.2 节后续能力），没有独立入口的必要。结果对比（/admin/compare）
// 是只读的多源对比查看页面，勾选多个已完成的历史 TestRun 并排展示，有独立
// 入口的必要，放进侧边栏。套件管理（/admin/suites）对应 10.2 节"套件克隆
// 管理界面"——suites/ 目录下出现第二个供应商套件（z-ai）后触发的引入条件。
const routes: IRoute[] = [
  {
    name: '模型登记',
    layout: '/admin',
    path: 'providers',
    icon: <MdPeopleAlt className="h-6 w-6" />,
  },
  {
    name: '套件管理',
    layout: '/admin',
    path: 'suites',
    icon: <MdLibraryBooks className="h-6 w-6" />,
  },
  {
    name: '发起测试任务',
    layout: '/admin',
    path: 'test-runs/new',
    icon: <MdPlayCircleOutline className="h-6 w-6" />,
  },
  {
    name: '结果对比',
    layout: '/admin',
    path: 'compare',
    icon: <MdCompareArrows className="h-6 w-6" />,
  },
  // 以下是没有侧边栏入口的详情页（hidden），只从列表/任务页跳进来。
  // 登记在这里是为了让顶栏面包屑和页面标题能取到正确名称——
  // utils/navigation.ts 的 findCurrentRoute 按数组顺序做前缀匹配，
  // 'test-runs/' 必须排在 'test-runs/new' 之后，否则 /admin/test-runs/new
  // 会被这条更宽的规则先截走。
  {
    name: '结果详情',
    layout: '/admin',
    path: 'test-runs/',
    icon: <MdPlayCircleOutline className="h-6 w-6" />,
    hidden: true,
  },
  {
    name: '自测报告',
    layout: '/admin',
    path: 'reports/',
    icon: <MdLibraryBooks className="h-6 w-6" />,
    hidden: true,
  },
];
export default routes;
