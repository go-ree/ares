<template>
  <div class="common-layout">
    <el-container>
      <!-- 顶部导航栏 -->
      <el-header class="header">
        <div class="header-left">
          <span class="project-title">ChaosCanvas</span>
        </div>
        <div class="header-right">
          <el-dropdown>
            <span class="user-info">
              <el-avatar :size="32">{{ userInitials }}</el-avatar>
              <span class="username">{{ userDisplayName }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item
                  v-if="authStore.user?.auth_source === 'bootstrap'"
                  :disabled="passwordSubmitting"
                  @click="openPasswordDialog"
                >
                  修改密码
                </el-dropdown-item>
                <el-dropdown-item :disabled="logoutLoading" @click="handleLogout">
                  {{ logoutLoading ? '正在退出…' : '退出登录' }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>
      <el-container>
        <!-- 侧边栏菜单 -->
        <el-aside :width="isCollapse ? '64px' : '200px'" class="aside">
          <el-menu
            :default-active="activeMenu"
            class="el-menu-vertical"
            :class="{ 'is-collapsed': isCollapse }"
            :router="true"
            :collapse="isCollapse"
            :unique-opened="!isCollapse"
            background-color="#fff"
            text-color="#303133"
            active-text-color="#409EFF"
          >
            <el-menu-item index="/">
              <el-icon><House /></el-icon>
              <span class="menu-text">首页</span>
            </el-menu-item>
            <el-sub-menu
              v-if="
                authStore.canAny([PERMISSIONS.APPLICATIONS_READ, PERMISSIONS.APPLICATIONS_WRITE])
              "
              index="/application"
            >
              <template #title>
                <el-icon><Collection /></el-icon>
                <span class="menu-text">应用管理</span>
              </template>
              <el-menu-item
                v-if="authStore.can(PERMISSIONS.APPLICATIONS_READ)"
                index="/application/list"
              >
                <el-icon><List /></el-icon>
                <span class="menu-text">应用列表</span>
              </el-menu-item>
              <el-menu-item
                v-if="authStore.can(PERMISSIONS.APPLICATIONS_WRITE)"
                index="/application/apply"
              >
                <el-icon><Edit /></el-icon>
                <span class="menu-text">应用申请</span>
              </el-menu-item>
            </el-sub-menu>
            <el-sub-menu
              v-if="authStore.canAny([PERMISSIONS.RELEASES_READ, PERMISSIONS.RELEASES_CREATE])"
              index="/publish"
            >
              <template #title>
                <el-icon><UploadFilled /></el-icon>
                <span class="menu-text">发布工具</span>
              </template>
              <el-menu-item v-if="authStore.can(PERMISSIONS.RELEASES_READ)" index="/publish/deploy">
                <el-icon><Upload /></el-icon>
                <span class="menu-text">服务发布</span>
              </el-menu-item>
              <el-menu-item
                v-if="authStore.can(PERMISSIONS.RELEASES_CREATE)"
                index="/publish/merge"
              >
                <el-icon><Link /></el-icon>
                <span class="menu-text">代码合并</span>
              </el-menu-item>
            </el-sub-menu>
            <el-sub-menu
              v-if="
                authStore.canAny([
                  PERMISSIONS.LOGS_READ,
                  PERMISSIONS.RELEASES_CREATE,
                  PERMISSIONS.KUBERNETES_READ,
                ])
              "
              index="/operation"
            >
              <template #title>
                <el-icon><Tools /></el-icon>
                <span class="menu-text">运维管理</span>
              </template>
              <el-menu-item v-if="authStore.can(PERMISSIONS.LOGS_READ)" index="/operation/log">
                <el-icon><Document /></el-icon>
                <span class="menu-text">日志查询</span>
              </el-menu-item>
              <el-menu-item
                v-if="
                  authStore.can(PERMISSIONS.APPLICATIONS_READ) &&
                  authStore.can(PERMISSIONS.RELEASES_CREATE)
                "
                index="/operation/batch-deploy"
              >
                <el-icon><Upload /></el-icon>
                <span class="menu-text">一键批量发布</span>
              </el-menu-item>
              <el-menu-item
                v-if="authStore.can(PERMISSIONS.KUBERNETES_READ)"
                index="/operation/monitor"
              >
                <el-icon><DataAnalysis /></el-icon>
                <span class="menu-text">监控面板</span>
              </el-menu-item>
            </el-sub-menu>
            <el-sub-menu index="/system">
              <template #title>
                <el-icon><Setting /></el-icon>
                <span class="menu-text">系统设置</span>
              </template>
              <el-menu-item
                v-if="authStore.can(PERMISSIONS.SYSTEM_SETTINGS_READ)"
                index="/system/settings"
              >
                <el-icon><Tools /></el-icon>
                <span class="menu-text">系统配置</span>
              </el-menu-item>
              <el-menu-item v-if="authStore.can(PERMISSIONS.USERS_READ)" index="/system/users">
                <el-icon><UserFilled /></el-icon>
                <span class="menu-text">用户与角色</span>
              </el-menu-item>
              <el-menu-item index="/system/version">
                <el-icon><InfoFilled /></el-icon>
                <span class="menu-text">版本信息</span>
              </el-menu-item>
            </el-sub-menu>
          </el-menu>
          <div class="aside-collapse-btn" @click="toggleCollapse">
            <svg
              :class="{ rotated: isCollapse }"
              viewBox="64 64 896 896"
              focusable="false"
              data-icon="menu-fold"
              width="22"
              height="22"
              fill="currentColor"
              aria-hidden="true"
            >
              <path
                d="M408 442h480c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8H408c-4.4 0-8 3.6-8 8v56c0 4.4 3.6 8 8 8zm-8 204c0 4.4 3.6 8 8 8h480c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8H408c-4.4 0-8 3.6-8 8v56zm504-486H120c-4.4 0-8 3.6-8 8v56c0 4.4 3.6-8 8-8h784c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8zm0 632H120c-4.4 0-8 3.6-8 8v56c0 4.4 3.6-8 8-8h784c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8zM115.4 518.9L271.7 642c5.8 4.6 14.4.5 14.4-6.9V388.9c0-7.4-8.5-11.5-14.4-6.9L115.4 505.1a8.74 8.74 0 000 13.8z"
              ></path>
            </svg>
            <span class="collapse-text" :class="{ hidden: isCollapse }">收起</span>
            <span class="collapse-text" :class="{ hidden: !isCollapse }">展开</span>
          </div>
        </el-aside>
        <!-- 主内容区 -->
        <el-main>
          <router-view />
        </el-main>
      </el-container>
    </el-container>

    <el-dialog
      v-model="passwordDialogVisible"
      title="修改密码"
      width="440px"
      :close-on-click-modal="false"
      :close-on-press-escape="!passwordSubmitting"
      :show-close="!passwordSubmitting"
      @closed="resetPasswordForm"
    >
      <el-alert
        title="修改成功后会撤销该账号的全部会话，需要使用新密码重新登录。"
        type="warning"
        :closable="false"
        show-icon
      />
      <el-form label-position="top" class="password-form" @submit.prevent="submitPasswordChange">
        <el-form-item label="当前密码" required>
          <el-input
            v-model="passwordForm.current"
            type="password"
            show-password
            autocomplete="current-password"
            :disabled="passwordSubmitting"
          />
        </el-form-item>
        <el-form-item label="新密码" required>
          <el-input
            v-model="passwordForm.next"
            type="password"
            show-password
            autocomplete="new-password"
            :disabled="passwordSubmitting"
          />
        </el-form-item>
        <el-form-item label="确认新密码" required>
          <el-input
            v-model="passwordForm.confirmation"
            type="password"
            show-password
            autocomplete="new-password"
            :disabled="passwordSubmitting"
            @keyup.enter="submitPasswordChange"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :disabled="passwordSubmitting" @click="passwordDialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="passwordSubmitting" @click="submitPasswordChange">
          确认修改
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
import { useRouter } from 'vue-router';
import { ElMessage } from 'element-plus';
import { getPasswordChangeErrorMessage } from '@/services/auth';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS } from '@/types/auth';
import {
  ArrowDown,
  House,
  Collection,
  List,
  Setting,
  Edit,
  Upload,
  UploadFilled,
  Link,
  Tools,
  Document,
  DataAnalysis,
  InfoFilled,
  UserFilled,
} from '@element-plus/icons-vue';

const router = useRouter();
const authStore = useAuthStore();
const isCollapse = ref(false);
const logoutLoading = ref(false);
const passwordDialogVisible = ref(false);
const passwordSubmitting = ref(false);
const passwordForm = ref({ current: '', next: '', confirmation: '' });
const activeMenu = computed(() => router.currentRoute.value.path);

// 定义响应式变量存储窗口宽度
const windowWidth = ref(window.innerWidth);
// 定义自动折叠的宽度阈值
const COLLAPSE_THRESHOLD = 1200;

// 监听窗口大小变化
const handleResize = () => {
  windowWidth.value = window.innerWidth;
  // 当窗口宽度小于阈值时自动折叠
  if (windowWidth.value < COLLAPSE_THRESHOLD) {
    isCollapse.value = true;
  } else {
    isCollapse.value = false;
  }
};

// 组件挂载时添加监听器
onMounted(() => {
  window.addEventListener('resize', handleResize);
  // 初始化时检查一次
  handleResize();
});

// 组件卸载时移除监听器
onUnmounted(() => {
  window.removeEventListener('resize', handleResize);
});

const userDisplayName = computed(() => {
  return authStore.user?.display_name || authStore.user?.username || '未登录';
});
const userInitials = computed(() => userDisplayName.value.trim().slice(0, 2).toUpperCase());

const toggleCollapse = () => {
  isCollapse.value = !isCollapse.value;
};

const handleLogout = async () => {
  if (logoutLoading.value) return;
  logoutLoading.value = true;
  try {
    await authStore.logout();
    await router.replace({ name: 'login' });
  } catch {
    ElMessage.error('退出登录失败，会话尚未确认撤销，请重试');
  } finally {
    logoutLoading.value = false;
  }
};

const resetPasswordForm = () => {
  passwordForm.value = { current: '', next: '', confirmation: '' };
};

const openPasswordDialog = () => {
  resetPasswordForm();
  passwordDialogVisible.value = true;
};

const submitPasswordChange = async () => {
  if (passwordSubmitting.value) return;
  const current = passwordForm.value.current;
  const next = passwordForm.value.next;
  if (!current || !next || !passwordForm.value.confirmation) {
    ElMessage.warning('请完整填写当前密码、新密码和确认密码');
    return;
  }
  const passwordBytes = new TextEncoder().encode(next).byteLength;
  if (passwordBytes < 12 || passwordBytes > 1024) {
    ElMessage.warning('新密码长度必须在 12 到 1024 字节之间');
    return;
  }
  if (next !== passwordForm.value.confirmation) {
    ElMessage.warning('两次输入的新密码不一致');
    return;
  }
  if (current === next) {
    ElMessage.warning('新密码不能与当前密码相同');
    return;
  }

  passwordSubmitting.value = true;
  try {
    await authStore.changePassword({ current_password: current, new_password: next });
    passwordDialogVisible.value = false;
    ElMessage.success('密码已更新，请重新登录');
    await router.replace({ name: 'login' });
  } catch (error) {
    ElMessage.error(getPasswordChangeErrorMessage(error));
  } finally {
    passwordSubmitting.value = false;
  }
};
</script>

<style scoped>
/* 整体布局容器样式 */
.common-layout {
  width: 100vw;
  height: 100vh;
  background: #f5f7fa;
  overflow: hidden; /* 防止出现双滚动条 */
}

/* 顶部导航栏样式 */
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 60px;
  background: #fff;
  box-shadow: 0 1px 4px rgba(0, 21, 41, 0.08);
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 1000;
}

/* 侧边栏样式 */
.aside {
  position: fixed;
  top: 60px; /* header的高度 */
  bottom: 0;
  left: 0;
  background: #fff;
  box-shadow: 2px 0 8px 0 rgba(29, 35, 41, 0.05);
  z-index: 999;
  transition: width 0.3s;
  display: flex;
  flex-direction: column;
}

/* 菜单样式 */
.el-menu-vertical {
  border-right: none;
  flex: 1;
  overflow: hidden; /* 防止菜单出现滚动条 */
}

.el-menu-vertical:not(.el-menu--collapse) {
  width: 200px;
}

/* 折叠按钮样式 */
.aside-collapse-btn {
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  border-top: 1px solid #f0f0f0;
  color: #909399;
  transition: all 0.3s;
  flex-shrink: 0; /* 防止按钮被压缩 */
}

/* 主内容区样式 */
:deep(.el-main) {
  margin-left: v-bind('isCollapse ? "64px" : "200px"');
  margin-top: 60px; /* header的高度 */
  padding: 20px;
  height: calc(100vh - 60px); /* 减去header高度 */
  background: #f5f7fa;
  overflow-y: auto; /* 允许垂直滚动 */
  overflow-x: hidden; /* 禁止水平滚动 */
}

/* 用户信息样式 */
.user-info {
  display: flex;
  align-items: center;
  cursor: pointer;
  padding: 0 12px;
  height: 100%;
}

.user-info:hover {
  background: rgba(0, 0, 0, 0.025);
}

.username {
  margin: 0 8px;
  font-size: 14px;
  color: #606266;
}

/* 项目标题样式 */
.project-title {
  font-size: 18px;
  font-weight: 600;
  color: #303133;
  margin-left: 20px;
}

/* 头部左右布局 */
.header-left,
.header-right {
  display: flex;
  align-items: center;
  height: 100%;
}
</style>
