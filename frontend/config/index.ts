import devConfig from './dev'
import prodConfig from './prod'
import testConfig from './test'

const env = process.env.NODE_ENV || 'development'

const configs = {
  development: devConfig,
  production: prodConfig,
  test: testConfig
}

export default configs[env as keyof typeof configs] 