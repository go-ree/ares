<template>
  <div id="app">
    <!-- 开发模式下的工具栏 -->
    <div v-if="isDevelopment" class="dev-toolbar">
      <div class="toolbar-content">
        <span>开发工具栏</span>
        <button @click="toggleInspector">检查元素</button>
      </div>
    </div>
    <router-view></router-view>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'

const isDevelopment = ref(import.meta.env.DEV)
const isInspectorActive = ref(false)

const toggleInspector = () => {
  isInspectorActive.value = !isInspectorActive.value
  // 这里可以添加元素检查的逻辑
}

onMounted(() => {
  if (isDevelopment.value) {
    console.log('开发工具栏已启用')
  }
})
</script>

<style>
.dev-toolbar {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  background: #2c3e50;
  color: white;
  padding: 8px 16px;
  z-index: 9999;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.toolbar-content {
  display: flex;
  gap: 16px;
  align-items: center;
}

.toolbar-content button {
  background: #42b983;
  border: none;
  padding: 4px 8px;
  border-radius: 4px;
  color: white;
  cursor: pointer;
}

.toolbar-content button:hover {
  background: #3aa876;
}
</style> 