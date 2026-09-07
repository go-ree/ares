<template>
  <div class="users-page">
    <el-card shadow="never">
      <template #header>
        <div class="page-header">
          <div>
            <h2>用户与角色</h2>
            <p>管理登录用户的内置角色与启用状态，权限变更会撤销该用户的现有会话。</p>
          </div>
          <el-button :loading="loading" @click="loadFirstPage">刷新</el-button>
        </div>
      </template>

      <el-alert
        v-if="!canWriteUsers"
        class="permission-alert"
        title="当前账号拥有只读权限，角色与状态修改操作已隐藏。"
        type="info"
        :closable="false"
        show-icon
      />

      <el-table
        v-loading="loading && users.length === 0"
        :data="users"
        row-key="id"
        empty-text="暂无用户"
      >
        <el-table-column label="用户" min-width="210">
          <template #default="{ row }">
            <div class="user-identity">
              <div>
                <span class="display-name">{{ row.display_name }}</span>
                <el-tag v-if="row.id === currentUserID" class="current-user-tag" size="small">
                  当前账号
                </el-tag>
              </div>
              <span class="username">{{ row.username }}</span>
              <span v-if="row.email" class="email">{{ row.email }}</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="来源" width="140">
          <template #default="{ row }">
            <el-tag :type="row.auth_source === 'oidc' ? 'success' : 'info'">
              {{ sourceLabel(row.auth_source) }}
            </el-tag>
          </template>
        </el-table-column>

        <el-table-column label="角色" min-width="190">
          <template #default="{ row }">
            <el-select
              v-if="canWriteUsers"
              :model-value="row.role"
              :disabled="isSaving(row.id)"
              :aria-label="`修改 ${row.username} 的角色`"
              @change="changeRole(row, $event)"
            >
              <el-option
                v-for="option in roleOptions"
                :key="option.value"
                :label="option.label"
                :value="option.value"
              />
            </el-select>
            <el-tag v-else :type="roleTagType(row.role)">{{ roleLabel(row.role) }}</el-tag>
          </template>
        </el-table-column>

        <el-table-column label="状态" width="150">
          <template #default="{ row }">
            <el-switch
              v-if="canWriteUsers"
              :model-value="row.enabled"
              :loading="isSaving(row.id)"
              :disabled="isSaving(row.id)"
              inline-prompt
              active-text="启用"
              inactive-text="停用"
              :aria-label="`切换 ${row.username} 的启用状态`"
              @change="changeEnabled(row, $event)"
            />
            <el-tag v-else :type="row.enabled ? 'success' : 'danger'">
              {{ row.enabled ? '已启用' : '已停用' }}
            </el-tag>
          </template>
        </el-table-column>

        <el-table-column label="最近登录" min-width="180">
          <template #default="{ row }">{{ formatDateTime(row.last_login_at) }}</template>
        </el-table-column>
      </el-table>

      <div v-if="hasMore" class="load-more">
        <el-button :loading="loadingMore" @click="loadNextPage">加载更多</el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import { useRouter } from 'vue-router';
import { getUserApiError, listUsers, updateUser } from '@/services/users';
import { useAuthStore } from '@/stores/auth';
import {
  PERMISSIONS,
  type AuthSource,
  type BuiltInRole,
  type ManagedUser,
  type UpdateManagedUserRequest,
} from '@/types/auth';

const PAGE_SIZE = 100;
const roleOptions: Array<{ value: BuiltInRole; label: string }> = [
  { value: 'viewer', label: '查看者' },
  { value: 'developer', label: '开发者' },
  { value: 'releaser', label: '发布者' },
  { value: 'admin', label: '管理员' },
];

const router = useRouter();
const authStore = useAuthStore();
const users = ref<ManagedUser[]>([]);
const loading = ref(false);
const loadingMore = ref(false);
const nextOffset = ref(0);
const hasMore = ref(false);
const savingUserIDs = ref(new Set<string>());

const canWriteUsers = computed(() => authStore.can(PERMISSIONS.USERS_WRITE));
const currentUserID = computed(() => authStore.user?.id || '');
const isSaving = (userID: string) => savingUserIDs.value.has(userID);

const roleLabel = (role: BuiltInRole) =>
  roleOptions.find(option => option.value === role)?.label || role;

const roleTagType = (role: BuiltInRole) => {
  if (role === 'admin') return 'danger';
  if (role === 'releaser') return 'warning';
  if (role === 'developer') return 'success';
  return 'info';
};

const sourceLabel = (source: AuthSource) => (source === 'oidc' ? 'OIDC' : '本地账号');

const formatDateTime = (value?: string) => {
  if (!value) return '尚未登录';
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '未知';
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
};

const loadFirstPage = async () => {
  if (loading.value || loadingMore.value) return;
  loading.value = true;
  try {
    const page = await listUsers(0, PAGE_SIZE);
    users.value = page.items;
    nextOffset.value = page.next_offset;
    hasMore.value = page.items.length === PAGE_SIZE;
  } catch (error) {
    ElMessage.error(getUserApiError(error).message || '用户列表加载失败');
  } finally {
    loading.value = false;
  }
};

const loadNextPage = async () => {
  if (loading.value || loadingMore.value || !hasMore.value) return;
  loadingMore.value = true;
  try {
    const page = await listUsers(nextOffset.value, PAGE_SIZE);
    const knownIDs = new Set(users.value.map(user => user.id));
    users.value.push(...page.items.filter(user => !knownIDs.has(user.id)));
    nextOffset.value = page.next_offset;
    hasMore.value = page.items.length === PAGE_SIZE;
  } catch (error) {
    ElMessage.error(getUserApiError(error).message || '更多用户加载失败');
  } finally {
    loadingMore.value = false;
  }
};

const setSaving = (userID: string, saving: boolean) => {
  const next = new Set(savingUserIDs.value);
  if (saving) next.add(userID);
  else next.delete(userID);
  savingUserIDs.value = next;
};

const applyUpdatedUser = (updated: ManagedUser) => {
  const index = users.value.findIndex(user => user.id === updated.id);
  if (index >= 0) users.value[index] = updated;
};

const updateManagedUser = async (user: ManagedUser, patch: UpdateManagedUserRequest) => {
  if (!canWriteUsers.value || isSaving(user.id)) return;
  setSaving(user.id, true);
  try {
    const updated = await updateUser(user.id, patch);
    applyUpdatedUser(updated);
    ElMessage.success(`用户 ${updated.username} 已更新`);

    if (updated.id === currentUserID.value) {
      authStore.invalidate('permissions_changed');
      await router.replace({ name: 'login' });
    }
  } catch (error) {
    const failure = getUserApiError(error);
    if (failure.status === 409) {
      ElMessage.error('至少需要保留一个已启用的管理员，本次修改未生效');
    } else {
      ElMessage.error(failure.message || '用户更新失败');
    }
  } finally {
    setSaving(user.id, false);
  }
};

const changeRole = async (user: ManagedUser, value: BuiltInRole) => {
  if (value === user.role) return;
  try {
    await ElMessageBox.confirm(
      `确认将“${user.display_name}”的角色从“${roleLabel(user.role)}”调整为“${roleLabel(value)}”吗？该用户的现有会话会被撤销。`,
      '确认调整角色',
      {
        type: 'warning',
        confirmButtonText: '确认调整',
        cancelButtonText: '取消',
      }
    );
  } catch {
    return;
  }
  await updateManagedUser(user, { role: value });
};

const changeEnabled = async (user: ManagedUser, value: string | number | boolean) => {
  const enabled = Boolean(value);
  if (enabled === user.enabled) return;
  try {
    await ElMessageBox.confirm(
      enabled
        ? `确认启用用户“${user.display_name}”吗？`
        : `确认停用用户“${user.display_name}”吗？该用户的现有会话会被撤销。`,
      enabled ? '确认启用用户' : '确认停用用户',
      {
        type: enabled ? 'info' : 'warning',
        confirmButtonText: enabled ? '确认启用' : '确认停用',
        cancelButtonText: '取消',
      }
    );
  } catch {
    return;
  }
  await updateManagedUser(user, { enabled });
};

onMounted(loadFirstPage);
</script>

<style scoped>
.users-page {
  width: 100%;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
}

.page-header h2 {
  margin: 0 0 8px;
  font-size: 20px;
}

.page-header p {
  margin: 0;
  color: #606266;
  line-height: 1.5;
}

.permission-alert {
  margin-bottom: 18px;
}

.user-identity {
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.4;
}

.display-name {
  color: #303133;
  font-weight: 600;
}

.current-user-tag {
  margin-left: 8px;
}

.username,
.email {
  color: #909399;
  font-size: 12px;
}

.load-more {
  display: flex;
  justify-content: center;
  padding-top: 18px;
}

@media (max-width: 768px) {
  .page-header {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
