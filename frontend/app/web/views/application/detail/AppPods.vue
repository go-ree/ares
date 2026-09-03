<template>
  <div class="app-pods">
    <el-card shadow="never" class="pods-card">
      <template #header>
        <div class="card-header">
          <span>Pod 实例</span>
          <span class="hint">预留：后续接入 K8s Pod 列表/详情/重启/日志/监控等能力。</span>
        </div>
      </template>

      <el-empty description="Pod 实例接入中（暂无数据源）" :image-size="90">
        <template #description>
          <div class="desc">
            这里会展示该应用在不同环境下的 Pod 实例列表（状态、节点、资源、时间），并支持快捷操作。
          </div>
        </template>
      </el-empty>

      <el-divider content-position="left">建议的数据结构（后端接入点）</el-divider>
      <el-descriptions :column="1" border>
        <el-descriptions-item label="列表接口">
          <code>GET /api/v1/apps/:appId/pods?env=:environment</code>
        </el-descriptions-item>
        <el-descriptions-item label="单 Pod 详情">
          <code>GET /api/v1/apps/:appId/pods/:podName?env=:environment</code>
        </el-descriptions-item>
        <el-descriptions-item label="操作">
          <code>POST /api/v1/apps/:appId/pods/:podName/restart</code>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>

<script setup lang="ts"></script>

<style scoped>
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.hint {
  font-size: 12px;
  color: #909399;
}

.desc {
  color: #606266;
  font-size: 13px;
  line-height: 1.5;
}

.pods-card {
  border-radius: 8px;
}
</style>
