<!-- 应用高级搜索组件 -->
<template>
  <div class="app-advanced-search">
    <el-form :model="searchForm" label-width="100px" class="search-form">
      <el-row :gutter="20">
        <el-col :span="8">
          <el-form-item label="APPID:">
            <el-input v-model.number="searchForm.app_id" placeholder="请输入APPID" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="应用名称:">
            <el-input v-model="searchForm.app_name" placeholder="请输入应用名称" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="应用中文名:">
            <el-input v-model="searchForm.app_name_cn" placeholder="请输入应用中文名" />
          </el-form-item>
        </el-col>
      </el-row>
      <el-row :gutter="20">
        <el-col :span="8">
          <el-form-item label="负责人:">
            <el-input v-model="searchForm.owner_cn" placeholder="请输入负责人" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="开发语言:">
            <el-select 
              v-model="searchForm.dev_language" 
              placeholder="请选择开发语言" 
              style="width: 100%"
              clearable
            >
              <el-option label="Java" :value="DevLanguage.JAVA" />
              <el-option label="Python" :value="DevLanguage.PYTHON" />
              <el-option label="Golang" :value="DevLanguage.GO" />
              <el-option label="Node.js" :value="DevLanguage.NODE" />
              <!-- <el-option label="PHP" :value="DevLanguage.PHP" /> -->
              <!-- <el-option label="其他" :value="DevLanguage.OTHER" /> -->
            </el-select>
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="Git仓库:">
            <el-input v-model="searchForm.git_url" placeholder="请输入Git仓库地址" />
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
import type { AppQueryParams } from '@/models/application'
import { DevLanguage, AppStatus } from '@/models/application'

// 定义搜索表单数据
const searchForm = reactive<Partial<AppQueryParams & { app_id?: number }>>({
  app_id: undefined,
  app_name: '',
  app_name_cn: '',
  owner_cn: '',
  dev_language: undefined,
  git_url: '',
  status: undefined,
  page_num: 1,
  page_size: 10
})

// 定义事件
const emit = defineEmits<{
  (e: 'search', params: Partial<AppQueryParams & { app_id?: number }>): void
}>()

// 搜索处理函数
const handleSearch = () => {
  // 过滤掉空值，但保留分页参数
  const params = Object.fromEntries(
    Object.entries(searchForm).filter(([key, value]) => {
      if (key === 'page_num' || key === 'page_size') return true
      if (typeof value === 'string') return value.trim() !== ''
      return value !== undefined
    })
  )
  emit('search', params)
}

// 重置处理函数
const handleReset = () => {
  Object.keys(searchForm).forEach(key => {
    const typedKey = key as keyof typeof searchForm
    if (key === 'page_num') {
      (searchForm[typedKey] as number) = 1
    } else if (key === 'page_size') {
      (searchForm[typedKey] as number) = 10
    } else if (typeof searchForm[typedKey] === 'string') {
      (searchForm[typedKey] as string) = ''
    } else if (typeof searchForm[typedKey] === 'number') {
      (searchForm[typedKey] as number | undefined) = undefined
    } else {
      searchForm[typedKey] = undefined
    }
  })
  emit('search', { page_num: 1, page_size: 10 })
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