import React from 'react';

// Icon Imports
import { MdPeopleAlt, MdPlayCircleOutline } from 'react-icons/md';

// 对应设计方案 10.3 节路由映射：模型登记（/admin/providers）、发起任务+
// 结果详情合并（/admin/test-runs/[id]）。报告下载（/admin/reports/[runId]）
// 从测试任务结果页跳转进入，不单独放进侧边栏一级导航——首版没有历史报告
// 列表页（10.2 节后续能力），没有独立入口的必要。
const routes = [
  {
    name: '模型登记',
    layout: '/admin',
    path: 'providers',
    icon: <MdPeopleAlt className="h-6 w-6" />,
  },
  {
    name: '发起测试任务',
    layout: '/admin',
    path: 'test-runs/new',
    icon: <MdPlayCircleOutline className="h-6 w-6" />,
  },
];
export default routes;
