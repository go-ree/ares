<!-- 应用列表页面 -->
<template>
  <div class="app-list-page">
    <h2>应用管理</h2>

    <!-- 使用高级搜索组件 -->
    <AppAdvancedSearch @search="handleAdvancedSearch" />

    <!-- 使用表格组件 -->
    <AppTable
      :app-list="appList"
      :loading="loading"
      :total="total"
      @edit="handleEdit"
      @delete="handleDelete"
      @detail="handleDetail"
      @page-change="handlePageChange"
      @size-change="handleSizeChange"
    />

    <!-- 详情对话框 -->
    <AppDetailDialog v-model:visible="detailDialogVisible" :app-id="currentAppId" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import AppTable from '@/components/application/AppTable.vue';
import AppAdvancedSearch from '@/components/application/AppAdvancedSearch.vue';
import AppDetailDialog from '@/components/application/AppDetailDialog.vue';
import { queryApps } from '@/services/application';
import type { AppInfo, AppQueryParams, PageResponse } from '@/models/application';

// 分页参数
const pageNum = ref(1);
const pageSize = ref(10);
const total = ref(0);

// 应用列表数据
const appList = ref<AppInfo[]>([]);

// 加载状态
const loading = ref(false);

// 保存当前的搜索条件
const currentSearchParams = ref<Partial<AppQueryParams>>({});

// 详情对话框状态
const detailDialogVisible = ref(false);
const currentAppId = ref<number>();

// 获取应用列表数据
const fetchAppList = async (params: Partial<AppQueryParams> = {}) => {
  loading.value = true;
  try {
    // 合并当前搜索条件和传入的参数
    const queryParams: AppQueryParams = {
      ...currentSearchParams.value, // 保持当前的搜索条件
      ...params, // 新的参数会覆盖旧的参数
      page_num: params.page_num ?? pageNum.value,
      page_size: params.page_size ?? pageSize.value,
    };

    const response = await queryApps(queryParams);
    if (response.data.code === 1) {
      // 检查响应状态码
      const apps = response.data.result?.apps;
      appList.value = Array.isArray(apps) ? apps : [];
      total.value = response.data.result?.total ?? 0;

      // 更新本地分页状态
      pageNum.value = response.data.result?.page_num ?? queryParams.page_num;
      pageSize.value = response.data.result?.page_size ?? queryParams.page_size;
    } else {
      ElMessage.error(response.data.message || '获取应用列表失败');
    }
  } catch (error) {
    console.error('获取应用列表失败:', error);
    ElMessage.error('获取应用列表失败');
    // 兜底：避免 AppTable 收到 null
    if (!Array.isArray(appList.value)) {
      appList.value = [];
    }
  } finally {
    loading.value = false;
  }
};

// 高级搜索处理函数
const handleAdvancedSearch = (searchParams: Partial<AppQueryParams>) => {
  // 更新当前搜索条件
  currentSearchParams.value = { ...searchParams };
  // 重置页码到第一页
  pageNum.value = 1;
  // 使用新的搜索条件获取数据
  fetchAppList({ ...searchParams, page_num: 1 });
};

// 处理分页变化
const handlePageChange = (newPage: number) => {
  pageNum.value = newPage;
  fetchAppList({ page_num: newPage });
};

// 处理每页条数变化
const handleSizeChange = (newSize: number) => {
  pageSize.value = newSize;
  pageNum.value = 1;
  fetchAppList({ page_size: newSize, page_num: 1 });
};

// 搜索处理函数
const handleSearch = (keyword: string) => {
  console.log('搜索关键词:', keyword);
  // 这里实现简单搜索逻辑
};

// 编辑处理函数
const handleEdit = (row: any) => {
  console.log('编辑应用:', row);
  // 这里可以实现编辑逻辑
};

// 删除处理函数
const handleDelete = (row: any) => {
  ElMessageBox.confirm(`确定要删除应用 ${row.app_name} 吗？`, '警告', {
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    type: 'warning',
  })
    .then(() => {
      // 这里实现删除逻辑
      ElMessage.success('删除成功');
    })
    .catch(() => {
      ElMessage.info('已取消删除');
    });
};

// 处理详情按钮点击
const handleDetail = (row: AppInfo) => {
  currentAppId.value = row.app_id;
  detailDialogVisible.value = true;
};

// 页面加载时获取数据
onMounted(() => {
  fetchAppList();
});
</script>

<style scoped>
.app-list-page {
  min-height: 100%;
  background-color: #f5f7fa;
}

h2 {
  margin: 0 0 20px 0;
  color: #303133;
  font-size: 20px;
  font-weight: 600;
}

/* 搜索组件样式 */
:deep(.app-advanced-search) {
  margin-bottom: 20px;
}

/* 表格组件样式 */
:deep(.app-table) {
  background-color: #fff;
  border-radius: 4px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
}

/* 分页器样式 */
:deep(.pagination-container) {
  padding: 16px 0;
  display: flex;
  justify-content: flex-end;
  background-color: #fff;
  border-top: 1px solid #ebeef5;
}
</style>
