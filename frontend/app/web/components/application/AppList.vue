<template>
  <div class="app-list">
    <!-- 搜索表单 -->
    <el-card class="search-card">
      <el-form :model="queryParams" ref="queryForm" :inline="true" class="search-form">
        <div class="form-row">
          <el-form-item label="应用名称：" prop="appName">
            <el-input v-model="queryParams.appName" placeholder="请输入应用名称" clearable style="width: 180px" />
          </el-form-item>
          <el-form-item label="中文名称：" prop="appNameCn">
            <el-input v-model="queryParams.appNameCn" placeholder="请输入中文名称" clearable style="width: 180px" />
          </el-form-item>
          <el-form-item label="负责人：" prop="owner">
            <el-input v-model="queryParams.owner" placeholder="请输入负责人" clearable style="width: 180px" />
          </el-form-item>
        </div>
        <div class="form-row">
          <el-form-item label="中文名：" prop="ownerCn">
            <el-input v-model="queryParams.ownerCn" placeholder="请输入负责人中文名" clearable style="width: 180px" />
          </el-form-item>
          <el-form-item label="开发语言：" prop="devLanguage">
            <el-select v-model="queryParams.devLanguage" placeholder="请选择开发语言" clearable style="width: 180px">
              <el-option
                v-for="item in devLanguageOptions"
                :key="item.value"
                :label="item.label"
                :value="item.value"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="Git仓库：" prop="gitUrl">
            <el-input v-model="queryParams.gitUrl" placeholder="请输入Git仓库地址" clearable style="width: 180px" />
          </el-form-item>
        </div>
        <div class="form-row button-row">
          <div class="button-group">
            <el-button type="primary" @click="handleQuery">查询</el-button>
            <el-button style="margin-left: 16px" @click="resetQuery">重置</el-button>
          </div>
        </div>
      </el-form>
    </el-card>

    <!-- 操作按钮 -->
    <div class="action-bar">
      <el-button type="primary" @click="handleAdd">新增应用</el-button>
      <el-button type="success" @click="handleDeploy">批量发布</el-button>
    </div>

    <!-- 数据表格 -->
    <el-card class="table-card">
      <el-table
        v-loading="loading"
        :data="appList"
        border
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <el-table-column type="selection" width="55" />
        <el-table-column prop="appName" label="应用名称" min-width="120" />
        <el-table-column prop="appNameCn" label="中文名称" min-width="120" />
        <el-table-column prop="owner" label="负责人" width="100" />
        <el-table-column prop="ownerCn" label="负责人中文名" width="120" />
        <el-table-column prop="devLanguage" label="开发语言" width="100">
          <template #default="{ row }">
            <el-tag>{{ row.devLanguage }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="descriptionCn" label="描述" min-width="200" show-overflow-tooltip />
        <el-table-column prop="gitUrl" label="Git仓库" min-width="200" show-overflow-tooltip />
        <el-table-column prop="status" label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="getStatusType(row.status)">
              {{ getStatusText(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最近发布" min-width="200">
          <template #default="{ row }">
            <div v-if="row.lastDeployTime">
              <div>时间：{{ row.lastDeployTime }}</div>
              <div>用户：{{ row.lastDeployUser }}</div>
              <div>状态：{{ row.lastDeployStatus }}</div>
              <div v-if="row.lastDeployComment">备注：{{ row.lastDeployComment }}</div>
            </div>
            <span v-else>暂无发布记录</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="250" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="handleEdit(row)">编辑</el-button>
            <el-button link type="primary" @click="handleConfig(row)">配置</el-button>
            <el-button link type="success" @click="handleDeploySingle(row)">发布</el-button>
            <el-button link type="danger" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination">
        <el-pagination
          v-model:current-page="queryParams.pageNum"
          v-model:page-size="queryParams.pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { AppStatus, DevLanguage } from '../../models/application'
import type { AppInfo, AppQueryParams } from '../../models/application'
import { getAppList, deleteApp } from '../../services/application'

// 查询参数
const queryParams = reactive<AppQueryParams>({
  pageNum: 1,
  pageSize: 10,
  appName: '',
  appNameCn: '',
  owner: '',
  ownerCn: '',
  devLanguage: undefined,
  gitUrl: ''
})

// 状态选项
const statusOptions = [
  { label: '待审核', value: AppStatus.PENDING },
  { label: '已通过', value: AppStatus.APPROVED },
  { label: '已拒绝', value: AppStatus.REJECTED },
  { label: '已部署', value: AppStatus.DEPLOYED }
]

// 开发语言选项
const devLanguageOptions = [
  { label: 'Java', value: DevLanguage.JAVA },
  { label: 'Python', value: DevLanguage.PYTHON },
  { label: 'Go', value: DevLanguage.GO },
  { label: 'Node.js', value: DevLanguage.NODE },
  { label: 'PHP', value: DevLanguage.PHP },
  { label: '其他', value: DevLanguage.OTHER }
]

// 数据列表
const appList = ref<AppInfo[]>([])
const total = ref(0)
const loading = ref(false)
const selectedApps = ref<AppInfo[]>([])

// 获取状态类型
const getStatusType = (status?: AppStatus) => {
  if (!status) return 'info'
  const map: Record<AppStatus, string> = {
    [AppStatus.PENDING]: 'warning',
    [AppStatus.APPROVED]: 'success',
    [AppStatus.REJECTED]: 'danger',
    [AppStatus.DEPLOYED]: 'info'
  }
  return map[status]
}

// 获取状态文本
const getStatusText = (status?: AppStatus) => {
  if (!status) return '未知'
  const map: Record<AppStatus, string> = {
    [AppStatus.PENDING]: '待审核',
    [AppStatus.APPROVED]: '已通过',
    [AppStatus.REJECTED]: '已拒绝',
    [AppStatus.DEPLOYED]: '已部署'
  }
  return map[status]
}

// 查询列表
const getList = async () => {
  loading.value = true
  try {
    const res = await getAppList(queryParams)
    appList.value = res.data.list
    total.value = res.data.total
  } catch (error) {
    console.error('获取应用列表失败:', error)
    ElMessage.error('获取应用列表失败')
  } finally {
    loading.value = false
  }
}

// 查询按钮点击
const handleQuery = () => {
  queryParams.pageNum = 1
  getList()
}

// 重置按钮点击
const resetQuery = () => {
  queryParams.appName = ''
  queryParams.appNameCn = ''
  queryParams.owner = ''
  queryParams.ownerCn = ''
  queryParams.devLanguage = undefined
  queryParams.gitUrl = ''
  handleQuery()
}

// 表格选择变化
const handleSelectionChange = (selection: AppInfo[]) => {
  selectedApps.value = selection
}

// 新增按钮点击
const handleAdd = () => {
  // TODO: 跳转到新增页面
}

// 编辑按钮点击
const handleEdit = (row: AppInfo) => {
  // TODO: 跳转到编辑页面
}

// 配置按钮点击
const handleConfig = (row: AppInfo) => {
  // TODO: 跳转到配置页面
}

// 单个应用发布
const handleDeploySingle = (row: AppInfo) => {
  // TODO: 跳转到发布页面
}

// 批量发布
const handleDeploy = () => {
  if (selectedApps.value.length === 0) {
    ElMessage.warning('请选择要发布的应用')
    return
  }
  // TODO: 跳转到批量发布页面
}

// 删除按钮点击
const handleDelete = (row: AppInfo) => {
  ElMessageBox.confirm(
    `确认删除应用"${row.appName}"吗？`,
    '警告',
    {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(async () => {
    try {
      await deleteApp(row.appId)
      ElMessage.success('删除成功')
      getList()
    } catch (error) {
      console.error('删除应用失败:', error)
      ElMessage.error('删除应用失败')
    }
  })
}

// 分页大小改变
const handleSizeChange = (val: number) => {
  queryParams.pageSize = val
  getList()
}

// 页码改变
const handleCurrentChange = (val: number) => {
  queryParams.pageNum = val
  getList()
}

// 初始化
onMounted(() => {
  getList()
})
</script>

<style scoped>
.app-list {
  padding: 20px;
}

.search-card {
  margin-bottom: 20px;
}

.search-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-row {
  display: flex;
  gap: 24px;
  align-items: flex-start;
}

.form-row :deep(.el-form-item) {
  margin-bottom: 0;
  margin-right: 0;
  flex: 1;
}

.form-row :deep(.el-form-item__label) {
  width: 90px;
  text-align: right;
  padding-right: 8px;
}

.button-row {
  justify-content: flex-end;
  margin-top: 8px;
}

.button-group {
  display: flex;
  align-items: center;
}

.action-bar {
  margin-bottom: 20px;
}

.table-card {
  margin-bottom: 20px;
}

.pagination {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}
</style> 