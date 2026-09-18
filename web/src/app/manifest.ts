import type { MetadataRoute } from 'next';

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: '模型自测台',
    short_name: '模型自测台',
    description:
      '面向多供应商 LLM 接入场景的自测执行与报告平台：功能用例断言、压测与可归档报告。',
    start_url: '/',
    display: 'standalone',
    theme_color: '#4318FF',
    background_color: '#ffffff',
    icons: [
      {
        src: '/favicon.ico',
        sizes: '64x64 32x32 24x24 16x16',
        type: 'image/x-icon',
      },
    ],
  };
}
