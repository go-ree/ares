<!-- 应用列表表格组件 -->
<template>
  <div class="app-table">
    <el-table :data="appList" style="width: 100%">
      <el-table-column prop="app_id" label="APPID" />
      <el-table-column prop="app_name" label="应用名称" />
      <el-table-column prop="app_name_cn" label="应用中文名称" />
      <el-table-column prop="owner" label="负责人" />
      <el-table-column prop="owner_cn" label="负责人中文名称" />
      <el-table-column prop="dev_language" label="开发语言" />
      <el-table-column prop="description_cn" label="中文描述信息" />
      <el-table-column prop="git_url" label="Git仓库地址" />
      <el-table-column prop="created_at" label="应用创建时间" />
      <el-table-column label="操作" width="200">
        <template #default="{ row }">
          <el-button type="primary" link @click="handleEdit(row)">编辑</el-button>
          <el-button type="danger" link @click="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
// 定义props
const props = defineProps<{
  appList: Array<{
    app_id: bigint
    app_name: string
    app_name_cn: string
    owner: string
    owner_cn: string
    dev_language: string
    description_cn: string
    git_url: string
    created_at: string
  }>
}>()

// 定义事件
const emit = defineEmits(['edit', 'delete'])

// 编辑处理函数
const handleEdit = (row: any) => {
  emit('edit', row)
}

// 删除处理函数
const handleDelete = (row: any) => {
  emit('delete', row)
}
</script>

<style scoped>
.app-table {
  margin-top: 20px;
}
</style> 