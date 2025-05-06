<template>
  <div class="app-list">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>应用列表</span>
          <el-button type="primary" @click="handleAdd">新增应用</el-button>
        </div>
      </template>
      
      <el-table :data="appList" style="width: 100%">
        <el-table-column prop="appId" label="应用ID" width="180" />
        <el-table-column prop="appName" label="应用名称" width="180" />
        <el-table-column prop="owner" label="负责人" />
        <el-table-column prop="devLanguage" label="开发语言" />
        <el-table-column prop="status" label="状态">
          <template #default="{ row }">
            <el-tag :type="getStatusType(row.status)">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200">
          <template #default="{ row }">
            <el-button-group>
              <el-button size="small" @click="handleEdit(row)">编辑</el-button>
              <el-button size="small" type="primary" @click="handleConfig(row)">配置</el-button>
              <el-button size="small" type="danger" @click="handleDelete(row)">删除</el-button>
            </el-button-group>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

// 模拟数据
const appList = ref([
  {
    appId: 'user-service',
    appName: '用户服务',
    owner: '张三',
    devLanguage: 'Java',
    status: 'RUNNING'
  },
  {
    appId: 'order-service',
    appName: '订单服务',
    owner: '李四',
    devLanguage: 'Go',
    status: 'STOPPED'
  },
  {
    appId: 'payment-service',
    appName: '支付服务',
    owner: '王五',
    devLanguage: 'Python',
    status: 'DEPLOYING'
  }
])

const getStatusType = (status: string) => {
  const map: Record<string, string> = {
    RUNNING: 'success',
    STOPPED: 'info',
    DEPLOYING: 'warning',
    ERROR: 'danger'
  }
  return map[status] || 'info'
}

const handleAdd = () => {
  console.log('新增应用')
}

const handleEdit = (row: any) => {
  console.log('编辑应用', row)
}

const handleConfig = (row: any) => {
  console.log('配置应用', row)
}

const handleDelete = (row: any) => {
  console.log('删除应用', row)
}
</script>

<style scoped>
.app-list {
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
</style> 