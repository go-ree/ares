<!-- 应用列表页面 -->
<template>
  <div class="app-list-page">
    <h2>应用管理</h2>
    
    <!-- 使用搜索组件 -->
    <AppSearch @search="handleSearch" />
    
    <!-- 使用表格组件 -->
    <AppTable 
      :app-list="appList" 
      @edit="handleEdit"
      @delete="handleDelete"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import AppSearch from '@/components/application/AppSearch.vue'
import AppTable from '@/components/application/AppTable.vue'

// 应用列表数据
const appList = ref([
  {app_id: 10000, app_name: 'ares', app_name_cn: '发布引擎',owner: 'yangyang.tian',owner_cn:'田洋杨',dev_language:'golang',description_cn:'发布引擎',git_url:'git@ttpai.cn',created_at:'2024'},
  {app_id: 10001, app_name: 'ares', app_name_cn: '发布引擎',owner: 'yangyang.tian',owner_cn:'田洋杨',dev_language:'golang',description_cn:'发布引擎',git_url:'git@ttpai.cn',created_at:'2024'},
  {app_id: 10002, app_name: 'ares', app_name_cn: '发布引擎',owner: 'yangyang.tian',owner_cn:'田洋杨',dev_language:'golang',description_cn:'发布引擎',git_url:'git@ttpai.cn',created_at:'2024'},
  {app_id: 10003, app_name: 'ares', app_name_cn: '发布引擎',owner: 'yangyang.tian',owner_cn:'田洋杨',dev_language:'golang',description_cn:'发布引擎',git_url:'git@ttpai.cn',created_at:'2024'},
  {app_id: 10004, app_name: 'ares', app_name_cn: '发布引擎',owner: 'yangyang.tian',owner_cn:'田洋杨',dev_language:'golang',description_cn:'发布引擎',git_url:'git@ttpai.cn',created_at:'2024'}
])

// 搜索处理函数
const handleSearch = (keyword: string) => {
  // 实现搜索逻辑
  console.log('搜索关键词:', keyword)
  // 这里可以调用API进行搜索
}

// 编辑处理函数
const handleEdit = (row: any) => {
  console.log('编辑应用:', row)
  // 这里可以实现编辑逻辑
}

// 删除处理函数
const handleDelete = (row: any) => {
  ElMessageBox.confirm(
    `确定要删除应用 ${row.name} 吗？`,
    '警告',
    {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(() => {
    // 这里实现删除逻辑
    ElMessage.success('删除成功')
  }).catch(() => {
    ElMessage.info('已取消删除')
  })
}

// 页面加载时获取数据
onMounted(() => {
  // 这里可以调用API获取应用列表
  console.log('页面加载完成，获取应用列表')
})
</script>

<style scoped>
.app-list-page {
  padding: 20px;
}

h2 {
  margin-bottom: 20px;
  color: #303133;
}
</style> 