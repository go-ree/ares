<!-- 应用列表表格组件 -->
<template>
  <div class="app-table">
    <el-table 
      :data="appList" 
      style="width: 100%" 
      v-loading="loading"
      :max-height="tableHeight"
    >
      <el-table-column prop="app_id" label="APPID" width="100" />
      <el-table-column prop="app_name" label="应用名称" min-width="150" />
      <el-table-column prop="app_name_cn" label="应用中文名称" min-width="150" />
      <el-table-column prop="description_cn" label="应用描述信息" min-width="200" show-overflow-tooltip />
      <el-table-column prop="owner_cn" label="负责人" width="120" />
      <el-table-column prop="dev_language" label="开发语言" width="100">
        <template #default="{ row }">
          {{ row.dev_language.toUpperCase() }}
        </template>
      </el-table-column>
      <el-table-column prop="git_url" label="Git仓库地址" min-width="200" show-overflow-tooltip />
      <el-table-column prop="created_at" label="应用创建时间" width="180">
        <template #default="{ row }">
          {{ new Date(row.created_at).toLocaleString() }}
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button type="primary" link @click="handleEdit(row)">编辑</el-button>
          <el-button type="danger" link @click="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    
    <!-- 分页组件 -->
    <div class="pagination-container">
      <el-pagination
        v-model:current-page="currentPage"
        v-model:page-size="pageSize"
        :page-sizes="[10, 20, 50, 100]"
        :total="total"
        layout="total, sizes, prev, pager, next, jumper"
        @size-change="handleSizeChange"
        @current-change="handleCurrentChange"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import type { AppInfo } from '@/models/application'
import { ref, onMounted, onUnmounted } from 'vue'

// 定义props
const props = defineProps<{
  appList: AppInfo[]
  loading?: boolean
  total: number
}>()

// 定义事件
const emit = defineEmits(['edit', 'delete', 'page-change', 'size-change'])

// 分页相关
const currentPage = ref(1)
const pageSize = ref(10)

// 表格高度计算
const tableHeight = ref(undefined)

// 编辑处理函数
const handleEdit = (row: AppInfo) => {
  emit('edit', row)
}

// 删除处理函数
const handleDelete = (row: AppInfo) => {
  emit('delete', row)
}

// 处理页码变化
const handleCurrentChange = (page: number) => {
  emit('page-change', page)
}

// 处理每页条数变化
const handleSizeChange = (size: number) => {
  emit('size-change', size)
}
</script>

<style scoped>
.app-table {
  padding: 20px;
}

/* 表格样式 */
:deep(.el-table) {
  --el-table-border-color: #ebeef5;
  --el-table-header-bg-color: #f5f7fa;
}

:deep(.el-table th) {
  font-weight: 600;
  color: #606266;
  background-color: var(--el-table-header-bg-color);
}

:deep(.el-table td) {
  color: #606266;
}

:deep(.el-table--enable-row-hover .el-table__body tr:hover > td) {
  background-color: #f5f7fa;
}

/* 分页器容器样式 */
.pagination-container {
  margin-top: 20px;
  padding: 10px 0;
  display: flex;
  justify-content: flex-end;
  background-color: #fff;
  border-top: 1px solid #ebeef5;
}

/* 移除按钮的焦点黑边 */
:deep(.el-button) {
  outline: none !important;
}

:deep(.el-button:focus) {
  outline: none !important;
}

:deep(.el-button:focus-visible) {
  outline: none !important;
}
</style> 