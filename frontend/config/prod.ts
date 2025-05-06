export default {
  port: process.env.PORT || 3000,
  apiBaseUrl: process.env.API_BASE_URL || 'https://api.chaoscanvas.com',
  database: {
    host: process.env.DB_HOST,
    port: parseInt(process.env.DB_PORT || '5432'),
    username: process.env.DB_USERNAME,
    password: process.env.DB_PASSWORD,
    database: process.env.DB_NAME
  }
} 