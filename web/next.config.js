/** @type {import('next').NextConfig} */

// const withTM = require('next-transpile-modules')(['@babel/preset-react']);
//   '@fullcalendar/common',
//   '@fullcalendar/common',
//   '@fullcalendar/daygrid',
//   '@fullcalendar/interaction',
//   '@fullcalendar/react',

const nextConfig = {
  // 明确锁定 workspace root 到本项目目录：用户主目录下恰好也有一个
  // package-lock.json（与本项目无关），Next.js 会把它误认成候选的
  // workspace root 并打印警告，不设置的话不影响功能，但每次构建都会有
  // 一条容易让人误以为项目配置有问题的警告噪音。
  outputFileTracingRoot: __dirname,
  // Docker 单容器部署产出精简运行时（.next/standalone），避免把整个
  // node_modules 打进最终镜像。
  output: 'standalone',
  basePath: process.env.NEXT_PUBLIC_BASE_PATH,
  assetPrefix: process.env.NEXT_PUBLIC_BASE_PATH,
  images: {
    domains: [
      'images.unsplash.com',
      'i.ibb.co',
      'scontent.fotp8-1.fna.fbcdn.net',
    ],
    // Make ENV
    unoptimized: true,
  },
  // 单容器部署下 api-server 与 Next.js 共享同一个对外端口：浏览器只
  // 打到当前 origin 的 /api/*（见 NEXT_PUBLIC_API_BASE_URL=''），由
  // Next.js 服务端转发到本容器内 loopback 上的 api-server（:8090）。
  async rewrites() {
    return [
      {
        source: '/api/:path*',
        destination: 'http://127.0.0.1:8090/api/:path*',
      },
    ];
  },
};

module.exports = nextConfig;
