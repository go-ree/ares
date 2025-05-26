<!-- 应用列表表格组件 -->
<template>
  <div class="app-table">
    <el-table :data="appList" style="width: 100%" v-loading="loading">
      <el-table-column prop="appId" label="APPID" />
      <el-table-column prop="appName" label="应用名称" />
      <el-table-column prop="appNameCn" label="应用中文名称" />
      <el-table-column prop="descriptionCn" label="应用描述信息" />
      <el-table-column prop="ownerCn" label="负责人" />
      <el-table-column prop="devLanguage" label="开发语言" />
      <el-table-column prop="gitUrl" label="Git仓库地址" />
      <el-table-column prop="createdAt" label="应用创建时间" />
      <el-table-column label="操作" width="200">
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
import { ref } from 'vue'

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
  margin-top: 20px;
}

.pagination-container {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
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