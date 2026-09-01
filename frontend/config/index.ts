import devConfig from './dev';
import prodConfig from './prod';
import testConfig from './test';

// 在前端环境中，使用 import.meta.env 替代 process.env
const env = (typeof window !== 'undefined' && import.meta?.env?.MODE) || 'development';

const configs = {
  development: devConfig,
  production: prodConfig,
  test: testConfig,
};

export default configs[env as keyof typeof configs];
