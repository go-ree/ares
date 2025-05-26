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
    // 从搜索参数中提取非分页参数
    const { page_num: searchPageNum, page_size: searchPageSize, ...searchParams } = params
    
    // 使用搜索参数中的分页参数（如果有），否则使用本地状态
    const queryParams: AppQueryParams = {
      page_num: searchPageNum ?? pageNum.value,
      page_size: searchPageSize ?? pageSize.value,
      ...searchParams
    }
    
    const response = await queryApps(queryParams)
    if (response.data.code === 1) {  // 检查响应状态码
      appList.value = response.data.result.apps
      total.value = response.data.result.total
      
      // 更新本地分页状态
      pageNum.value = response.data.result.page_num
      pageSize.value = response.data.result.page_size
    } else {
      ElMessage.error(response.data.msg || '获取应用列表失败')
    }
  } catch (error) {
    console.error('获取应用列表失败:', error)
    ElMessage.error('获取应用列表失败')
  } finally {
    loading.value = false
  }
}

// 高级搜索处理函数
const handleAdvancedSearch = (searchParams: Partial<AppQueryParams>) => {
  // 不再需要手动重置页码，因为搜索参数中已经包含了分页信息
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