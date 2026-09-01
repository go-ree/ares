#!/usr/bin/env node

/**
 * ChaosCanvas 应用入口文件
 * 用于Jenkins构建流程和Docker容器启动
 */

import { spawn } from 'child_process';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

// 设置环境变量
process.env.NODE_ENV = process.env.NODE_ENV || 'development';

// 获取启动参数
const args = process.argv.slice(2);
const command = args[0] || 'start:dev';

console.log(`[${new Date().toISOString()}] 启动 ChaosCanvas 应用`);
console.log(`[${new Date().toISOString()}] 环境: ${process.env.NODE_ENV}`);
console.log(`[${new Date().toISOString()}] 命令: ${command}`);
console.log(`[${new Date().toISOString()}] 工作目录: ${process.cwd()}`);

// 启动应用
const child = spawn('npm', ['run', command], {
  stdio: 'inherit',
  shell: true,
  env: {
    ...process.env,
    NODE_ENV: process.env.NODE_ENV,
  },
});

child.on('error', error => {
  console.error(`[${new Date().toISOString()}] 启动失败:`, error);
  process.exit(1);
});

child.on('exit', code => {
  console.log(`[${new Date().toISOString()}] 应用退出，退出码: ${code}`);
  process.exit(code);
});

// 处理进程信号
process.on('SIGTERM', () => {
  console.log(`[${new Date().toISOString()}] 收到 SIGTERM 信号，正在关闭应用...`);
  child.kill('SIGTERM');
});

process.on('SIGINT', () => {
  console.log(`[${new Date().toISOString()}] 收到 SIGINT 信号，正在关闭应用...`);
  child.kill('SIGINT');
});
