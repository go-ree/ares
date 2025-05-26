<!-- 应用列表页面 -->
<template>
  <div class="app-list-page">
    <h2>应用管理</h2>
    
    <!-- 使用高级搜索组件 -->
    <AppAdvancedSearch @search="handleAdvancedSearch" />

    <!-- 使用表格组件 -->
    <AppTable 
      :app-list="appList" 
      :loading="loading"
      :total="total"
      @edit="handleEdit"
      @delete="handleDelete"
      @page-change="handlePageChange"
      @size-change="handleSizeChange"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import AppTable from '@/components/application/AppTable.vue'
import AppAdvancedSearch from '@/components/application/AppAdvancedSearch.vue'
import { queryApps } from '@/services/application'
import type { AppInfo, AppQueryParams, PageResponse } from '@/models/application'

// 分页参数
const pageNum = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 应用列表数据
const appList = ref<AppInfo[]>([])

// 加载状态
const loading = ref(false)

// 获取应用列表数据
const fetchAppList = async (params: Partial<AppQueryParams> = {}) => {
  loading.value = true
  try {
    const queryParams: AppQueryParams = {
      pageNum: pageNum.value,
      pageSize: pageSize.value,
      ...params
    }
    const response = await queryApps(queryParams)
    appList.value = response.data.list
    total.value = response.data.total
  } catch (error) {
    console.error('获取应用列表失败:', error)
    ElMessage.error('获取应用列表失败')
  } finally {
    loading.value = false
  }
}

// 高级搜索处理函数
const handleAdvancedSearch = (searchParams: Partial<AppQueryParams>) => {
  pageNum.value = 1 // 重置页码
  fetchAppList(searchParams)
}

// 处理分页变化
const handlePageChange = (newPage: number) => {
  pageNum.value = newPage
  fetchAppList()
}

// 处理每页条数变化
const handleSizeChange = (newSize: number) => {
  pageSize.value = newSize
  pageNum.value = 1
  fetchAppList()
}

// 搜索处理函数
const handleSearch = (keyword: string) => {
  console.log('搜索关键词:', keyword)
  // 这里实现简单搜索逻辑
}

// 编辑处理函数
const handleEdit = (row: any) => {
  console.log('编辑应用:', row)
  // 这里可以实现编辑逻辑
}

// 删除处理函数
const handleDelete = (row: any) => {
  ElMessageBox.confirm(
    `确定要删除应用 ${row.app_name} 吗？`,
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
  fetchAppList()
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