const Footer = () => {
  return (
    <div className="flex w-full flex-col items-center justify-between gap-2 px-1 pb-8 pt-3 text-sm text-gray-600 lg:px-8 xl:flex-row">
      <span>© {new Date().getFullYear()} 模型自测台 · Model Testbed</span>
      <span>
        仅供 we2ai.com 模型接入自测使用；数据与报告保存在本机 / 内网部署环境
      </span>
    </div>
  );
};

export default Footer;
