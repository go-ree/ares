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
              <el-avatar :size="32" :src="user.avatar" />
              <span class="username">{{ user.name }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item>修改密码</el-dropdown-item>
                <el-dropdown-item divided @click="handleLogout">退出登录</el-dropdown-item>
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
            :router="true"
            :collapse="isCollapse"
            background-color="#fff"
            text-color="#303133"
            active-text-color="#409EFF"
          >
            <el-menu-item index="/">
              <el-icon><House /></el-icon>
              <span v-if="!isCollapse">首页</span>
            </el-menu-item>
            <el-sub-menu index="/application">
              <template #title>
                <el-icon><Collection /></el-icon>
                <span v-if="!isCollapse">应用管理</span>
              </template>
              <el-menu-item index="/application/list">
                <el-icon><List /></el-icon>
                <span v-if="!isCollapse">应用信息查询</span>
              </el-menu-item>
              <el-menu-item index="/application/config">
                <el-icon><Setting /></el-icon>
                <span v-if="!isCollapse">应用配置</span>
              </el-menu-item>
              <el-menu-item index="/application/apply">
                <el-icon><Edit /></el-icon>
                <span v-if="!isCollapse">应用申请</span>
              </el-menu-item>
            </el-sub-menu>
            <el-sub-menu index="/publish">
              <template #title>
                <el-icon><UploadFilled /></el-icon>
                <span v-if="!isCollapse">发布工具</span>
              </template>
              <el-menu-item index="/publish/deploy">
                <el-icon><Upload /></el-icon>
                <span v-if="!isCollapse">服务发布</span>
              </el-menu-item>
              <el-menu-item index="/publish/merge">
                <el-icon><Link /></el-icon>
                <span v-if="!isCollapse">代码合并</span>
              </el-menu-item>
            </el-sub-menu>
            <el-sub-menu index="/operation">
              <template #title>
                <el-icon><Tools /></el-icon>
                <span v-if="!isCollapse">运维管理</span>
              </template>
              <el-menu-item index="/operation/log">
                <el-icon><Document /></el-icon>
                <span v-if="!isCollapse">日志查询</span>
              </el-menu-item>
              <el-menu-item index="/operation/monitor">
                <el-icon><DataAnalysis /></el-icon>
                <span v-if="!isCollapse">监控面板</span>
              </el-menu-item>
            </el-sub-menu>
            <el-sub-menu index="/system">
              <template #title>
                <el-icon><Setting /></el-icon>
                <span v-if="!isCollapse">系统设置</span>
              </template>
              <el-menu-item index="/system/settings">
                <el-icon><Tools /></el-icon>
                <span v-if="!isCollapse">系统配置</span>
              </el-menu-item>
              <el-menu-item index="/system/version">
                <el-icon><InfoFilled /></el-icon>
                <span v-if="!isCollapse">版本信息</span>
              </el-menu-item>
            </el-sub-menu>
          </el-menu>
          <div class="aside-collapse-btn" @click="toggleCollapse">
            <svg viewBox="64 64 896 896" focusable="false" data-icon="menu-fold" width="22" height="22" fill="currentColor" aria-hidden="true">
              <path d="M408 442h480c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8H408c-4.4 0-8 3.6-8 8v56c0 4.4 3.6 8 8 8zm-8 204c0 4.4 3.6 8 8 8h480c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8H408c-4.4 0-8 3.6-8 8v56zm504-486H120c-4.4 0-8 3.6-8 8v56c0 4.4 3.6-8 8-8h784c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8zm0 632H120c-4.4 0-8 3.6-8 8v56c0 4.4 3.6-8 8-8h784c4.4 0 8-3.6 8-8v-56c0-4.4-3.6-8-8-8zM115.4 518.9L271.7 642c5.8 4.6 14.4.5 14.4-6.9V388.9c0-7.4-8.5-11.5-14.4-6.9L115.4 505.1a8.74 8.74 0 000 13.8z"></path>
            </svg>
            <span v-if="!isCollapse" class="collapse-text">收起</span>
            <span v-else class="collapse-text">展开</span>
          </div>
        </el-aside>
        <!-- 主内容区 -->
        <el-main>
          <router-view />
        </el-main>
      </el-container>
    </el-container>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
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
  InfoFilled
} from '@element-plus/icons-vue'

const router = useRouter()
const isCollapse = ref(false)
const activeMenu = computed(() => router.currentRoute.value.path)

const user = ref({
  name: '测试用户',
  avatar: 'https://api.dicebear.com/7.x/miniavs/svg?seed=1'
})

const toggleCollapse = () => {
  isCollapse.value = !isCollapse.value
}

const handleLogout = () => {
  router.push('/login')
}
</script>

<style scoped>
.common-layout {
  width: 100vw;
  height: 100vh;
  background: #f5f7fa;
}
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 60px;
  background: #fff;
  border-bottom: 1px solid #ebeef5;
  padding: 0 24px;
}
.project-title {
  font-size: 20px;
  font-weight: bold;
  color: #409EFF;
}
.header-right {
  display: flex;
  align-items: center;
}
.user-info {
  display: flex;
  align-items: center;
  cursor: pointer;
}
.username {
  margin: 0 8px;
  color: #303133;
}
.aside {
  display: flex;
  flex-direction: column;
  background: #fff;
  border-right: 1px solid #ebeef5;
  min-height: 100vh;
  position: relative;
  transition: width 0.2s;
  box-sizing: border-box;
  padding-bottom: 48px;
}
.el-menu-vertical {
  border-right: none;
  flex: 1 1 0%;
  min-height: 0;
  overflow-y: auto;
}
.aside-collapse-btn {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  border-top: 1px solid #ebeef5;
  background: #fff;
  transition: background 0.2s;
  z-index: 10;
}
.aside-collapse-btn:hover {
  background: #f5f7fa;
}
.collapse-text {
  margin-left: 8px;
  color: #909399;
  font-size: 14px;
}
</style> 