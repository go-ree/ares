<!-- 应用高级搜索组件 -->
<template>
  <div class="app-advanced-search">
    <el-form :model="searchForm" label-width="100px" class="search-form">
      <el-row :gutter="20">
        <el-col :span="6">
          <el-form-item label="应用名称">
            <el-input v-model="searchForm.app_name" placeholder="请输入应用名称" />
          </el-form-item>
        </el-col>
        <el-col :span="6">
          <el-form-item label="负责人">
            <el-input v-model="searchForm.owner" placeholder="请输入负责人" />
          </el-form-item>
        </el-col>
        <el-col :span="6">
          <el-form-item label="开发语言">
            <el-select v-model="searchForm.dev_language" placeholder="请选择开发语言" style="width: 100%">
              <el-option label="Golang" value="golang" />
              <el-option label="Java" value="java" />
              <el-option label="Python" value="python" />
              <el-option label="Node.js" value="node.js" />
            </el-select>
          </el-form-item>
        </el-col>
        <el-col :span="6">
          <el-form-item label="创建时间">
            <el-date-picker
              v-model="searchForm.created_at"
              type="daterange"
              range-separator="至"
              start-placeholder="开始日期"
              end-placeholder="结束日期"
              style="width: 100%"
            />
          </el-form-item>
        </el-col>
      </el-row>
      <el-row>
        <el-col :span="24" class="search-buttons">
          <el-button type="primary" @click="handleSearch">搜索</el-button>
          <el-button @click="handleReset">重置</el-button>
        </el-col>
      </el-row>
    </el-form>
  </div>
</template>

<script setup lang="ts">
import { reactive } from 'vue'

// 定义搜索表单数据
const searchForm = reactive({
  app_name: '',
  owner: '',
  dev_language: '',
  created_at: []
})

// 定义事件
const emit = defineEmits(['search'])

// 搜索处理函数
const handleSearch = () => {
  emit('search', { ...searchForm })
}

// 重置处理函数
const handleReset = () => {
  searchForm.app_name = ''
  searchForm.owner = ''
  searchForm.dev_language = ''
  searchForm.created_at = []
  emit('search', { ...searchForm })
}
</script>

<style scoped>
.app-advanced-search {
  background-color: #fff;
  padding: 20px;
  border-radius: 4px;
  margin-bottom: 20px;
}

.search-form {
  width: 100%;
}

.search-buttons {
  text-align: right;
  margin-top: 10px;
}

:deep(.el-form-item) {
  margin-bottom: 18px;
}
</style> 