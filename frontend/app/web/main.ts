import { createApp } from 'vue';
import { createPinia } from 'pinia';
import ElementPlus from 'element-plus';
import 'element-plus/dist/index.css';
import '../../src/style.css';
import App from './App.vue';
import zhCn from 'element-plus/es/locale/lang/zh-cn';
import router from './routes';
import { useUserStore } from './stores/user';

// 创建应用实例
const app = createApp(App);

// 创建 Pinia 实例
const pinia = createPinia();

// 使用插件
app.use(pinia);
app.use(ElementPlus, {
  locale: zhCn,
});
app.use(router);

// 恢复用户信息
const userStore = useUserStore();
userStore.restoreUserInfo();

// 挂载应用
app.mount('#app');
