import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const source = (path: string) => readFileSync(resolve(process.cwd(), path), 'utf8');

describe('Ares 品牌标识', () => {
  it('页面标题、布局和首页统一显示 Ares', () => {
    expect(source('index.html')).toContain('<title>Ares</title>');
    expect(source('app/web/components/layout/MainLayout.vue')).toContain(
      '<span class="project-title">Ares</span>'
    );
    expect(source('app/web/views/Home.vue')).toContain('<div class="welcome-title">Ares</div>');
  });

  it('包元数据与锁文件名称一致', () => {
    const pkg = JSON.parse(source('package.json'));
    const lock = JSON.parse(source('package-lock.json'));
    expect(pkg.name).toBe('ares');
    expect(lock.name).toBe(pkg.name);
    expect(lock.packages[''].name).toBe(pkg.name);
  });

  it('开发与生产健康检查使用相同的服务名称', () => {
    expect(source('config/vite.config.ts').match(/service: 'ares'/g)).toHaveLength(2);
    expect(source('nginx.conf')).toContain('"service":"ares"');
  });
});
