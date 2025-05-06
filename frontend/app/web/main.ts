import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import App from './App.vue'
import router from './routes'

// 创建应用实例
const app = createApp(App)

// 使用插件
app.use(ElementPlus)
app.use(router)

// 挂载应用
app.mount('#app') 