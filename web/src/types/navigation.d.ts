export interface IRoute {
  name: string;
  layout: string;
  icon: JSX.Element | string;
  items?: IRoute[];
  path: string;
  secondary?: boolean | undefined;
  /** true 表示不在侧边栏显示，仅用于面包屑/标题解析（详情页） */
  hidden?: boolean;
}
