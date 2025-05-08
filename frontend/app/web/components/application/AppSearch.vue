<!-- 应用搜索组件 -->
<template>
  <div class="app-search">
    <el-input
      v-model="searchText"
      placeholder="请输入应用名称"
      class="search-input"
      @input="handleSearch"
    >
      <template #prefix>
        <el-icon><Search /></el-icon>
      </template>
    </el-input>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Search } from '@element-plus/icons-vue'

const searchText = ref('')

// 定义事件
const emit = defineEmits(['search'])

// 搜索处理函数
const handleSearch = () => {
  emit('search', searchText.value)
}
</script>

<style scoped>
.app-search {
  margin-bottom: 20px;
}

.search-input {
  width: 300px;
}
</style> 