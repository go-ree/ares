<template>
  <div class="login-container">
    <el-card class="login-card" v-loading="optionsLoading">
      <template #header>
        <div class="login-heading">
          <h2>Ares 登录</h2>
          <p>身份与权限由服务端会话确认。</p>
        </div>
      </template>

      <el-alert
        v-if="pageError"
        :title="pageError"
        type="error"
        :closable="false"
        show-icon
        class="section-gap"
      />

      <el-button
        v-if="authOptions?.oidc_enabled"
        type="primary"
        size="large"
        class="full-width section-gap"
        @click="startOidcLogin"
      >
        使用组织账号登录
      </el-button>

      <template v-if="authOptions?.local_login_enabled">
        <el-divider v-if="authOptions.oidc_enabled">本地恢复管理员</el-divider>
        <el-form class="login-form" @submit.prevent="handleLocalLogin">
          <el-form-item>
            <el-input
              v-model="loginForm.username"
              autocomplete="username"
              maxlength="128"
              placeholder="用户名"
            />
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="loginForm.password"
              type="password"
              show-password
              autocomplete="current-password"
              placeholder="密码"
              @keyup.enter="handleLocalLogin"
            />
          </el-form-item>
          <el-button
            native-type="submit"
            class="full-width"
            :loading="loginLoading"
            :disabled="!canSubmitLogin || bootstrapLoading"
            @click="handleLocalLogin"
          >
            本地登录
          </el-button>
        </el-form>
      </template>

      <template v-if="authOptions?.bootstrap_available">
        <el-divider>首次部署管理员</el-divider>
        <el-alert
          title="Bootstrap 只能成功一次；完成后请从部署环境中删除 Bootstrap Token。"
          type="warning"
          :closable="false"
          show-icon
          class="section-gap"
        />
        <el-form class="login-form" @submit.prevent="handleBootstrap">
          <el-form-item :validate-status="bootstrapFieldError('bootstrap_token') ? 'error' : ''">
            <div class="bootstrap-field">
              <el-input
                v-model="bootstrapForm.bootstrap_token"
                type="password"
                autocomplete="off"
                placeholder="Bootstrap Token"
              />
              <span v-if="bootstrapFieldError('bootstrap_token')" class="field-error" role="alert">
                {{ bootstrapFieldError('bootstrap_token') }}
              </span>
            </div>
          </el-form-item>
          <el-form-item :validate-status="bootstrapFieldError('username') ? 'error' : ''">
            <div class="bootstrap-field">
              <el-input
                v-model="bootstrapForm.username"
                autocomplete="username"
                maxlength="64"
                placeholder="管理员用户名"
              />
              <span class="field-hint">
                3–64 个字符，以字母或数字开头，仅可包含字母、数字、点、下划线和连字符。
              </span>
              <span v-if="bootstrapFieldError('username')" class="field-error" role="alert">
                {{ bootstrapFieldError('username') }}
              </span>
            </div>
          </el-form-item>
          <el-form-item :validate-status="bootstrapFieldError('display_name') ? 'error' : ''">
            <div class="bootstrap-field">
              <el-input
                v-model="bootstrapForm.display_name"
                autocomplete="name"
                maxlength="255"
                placeholder="显示名称"
              />
              <span class="field-hint">不能为空，UTF-8 编码后不能超过 255 字节。</span>
              <span v-if="bootstrapFieldError('display_name')" class="field-error" role="alert">
                {{ bootstrapFieldError('display_name') }}
              </span>
            </div>
          </el-form-item>
          <el-form-item :validate-status="bootstrapFieldError('password') ? 'error' : ''">
            <div class="bootstrap-field">
              <el-input
                v-model="bootstrapForm.password"
                type="password"
                show-password
                autocomplete="new-password"
                placeholder="管理员密码"
              />
              <span class="field-hint">
                UTF-8 编码后须为 12–1024 字节；中文及部分符号会占多个字节。
              </span>
              <span v-if="bootstrapFieldError('password')" class="field-error" role="alert">
                {{ bootstrapFieldError('password') }}
              </span>
            </div>
          </el-form-item>
          <el-form-item
            :validate-status="bootstrapFieldError('password_confirmation') ? 'error' : ''"
          >
            <div class="bootstrap-field">
              <el-input
                v-model="bootstrapPasswordConfirmation"
                type="password"
                show-password
                autocomplete="new-password"
                placeholder="再次输入管理员密码"
                @keyup.enter="handleBootstrap"
              />
              <span
                v-if="bootstrapFieldError('password_confirmation')"
                class="field-error"
                role="alert"
              >
                {{ bootstrapFieldError('password_confirmation') }}
              </span>
            </div>
          </el-form-item>
          <el-button
            native-type="submit"
            type="primary"
            class="full-width"
            :loading="bootstrapLoading"
            :disabled="bootstrapLoading || loginLoading"
          >
            创建首次管理员并登录
          </el-button>
        </el-form>
      </template>

      <el-empty
        v-if="authOptions && !hasLoginMethod"
        :image-size="72"
        description="服务端尚未启用可用的登录方式"
      />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import axios from 'axios';
import { useAuthStore } from '@/stores/auth';
import { oidcStartURL } from '@/services/auth';
import type { ApiEnvelope } from '@/types/auth';
import { normalizeReturnTo } from '@/utils/return-to';

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const optionsLoading = ref(false);
const loginLoading = ref(false);
const bootstrapLoading = ref(false);
const pageError = ref('');

const loginForm = reactive({ username: '', password: '' });
const bootstrapForm = reactive({
  bootstrap_token: '',
  username: '',
  display_name: '',
  password: '',
});
const bootstrapPasswordConfirmation = ref('');
const bootstrapValidationVisible = ref(false);

type BootstrapField =
  | 'bootstrap_token'
  | 'username'
  | 'display_name'
  | 'password'
  | 'password_confirmation';

const bootstrapUsernamePattern = /^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$/;
const utf8ByteLength = (value: string) => new TextEncoder().encode(value).length;

const authOptions = computed(() => authStore.options);
const returnTo = computed(() => normalizeReturnTo(route.query.redirect));
const hasLoginMethod = computed(
  () =>
    Boolean(authOptions.value?.oidc_enabled) ||
    Boolean(authOptions.value?.local_login_enabled) ||
    Boolean(authOptions.value?.bootstrap_available)
);
const canSubmitLogin = computed(
  () => Boolean(loginForm.username.trim()) && Boolean(loginForm.password)
);
const bootstrapValidationErrors = computed<Partial<Record<BootstrapField, string>>>(() => {
  const errors: Partial<Record<BootstrapField, string>> = {};
  const username = bootstrapForm.username.trim();
  const displayName = bootstrapForm.display_name.trim();

  if (!bootstrapForm.bootstrap_token) {
    errors.bootstrap_token = '请输入部署环境中配置的 Bootstrap Token';
  }
  if (!bootstrapUsernamePattern.test(username)) {
    errors.username =
      '用户名须为 3–64 个字符，以字母或数字开头，且仅含字母、数字、点、下划线或连字符';
  }
  if (!displayName) {
    errors.display_name = '显示名称不能为空';
  } else if (utf8ByteLength(displayName) > 255) {
    errors.display_name = '显示名称的 UTF-8 编码不能超过 255 字节';
  }

  const passwordBytes = utf8ByteLength(bootstrapForm.password);
  if (passwordBytes < 12 || passwordBytes > 1024) {
    errors.password = '管理员密码的 UTF-8 编码须为 12–1024 字节';
  }
  if (!bootstrapPasswordConfirmation.value) {
    errors.password_confirmation = '请再次输入管理员密码';
  } else if (bootstrapForm.password !== bootstrapPasswordConfirmation.value) {
    errors.password_confirmation = '两次输入的管理员密码不一致';
  }
  return errors;
});

const bootstrapFieldError = (field: BootstrapField) =>
  bootstrapValidationVisible.value ? bootstrapValidationErrors.value[field] || '' : '';

const authErrorMessage = (error: unknown, fallback: string) => {
  if (!axios.isAxiosError(error)) return fallback;
  switch (error.response?.status) {
    case 401:
      return '用户名或密码错误';
    case 409:
      return '首次管理员已经创建，请使用现有账号登录';
    case 429:
      return '尝试次数过多，请稍后再试';
    case 503:
      return '认证服务暂时不可用，请稍后再试';
    default:
      return fallback;
  }
};

const bootstrapErrorMessage = (error: unknown, fallback: string) => {
  if (axios.isAxiosError<ApiEnvelope<unknown>>(error) && error.response?.status === 400) {
    const response = error.response.data;
    const details = typeof response?.error === 'string' ? response.error.trim() : '';
    const message = typeof response?.message === 'string' ? response.message.trim() : '';
    return details || message || fallback;
  }
  return authErrorMessage(error, fallback);
};

const finishLogin = async () => {
  await router.replace(returnTo.value);
};

const startOidcLogin = () => {
  window.location.assign(oidcStartURL(returnTo.value));
};

const handleLocalLogin = async () => {
  if (!canSubmitLogin.value || loginLoading.value) return;
  pageError.value = '';
  loginLoading.value = true;
  try {
    const authenticated = await authStore.login({
      username: loginForm.username.trim(),
      password: loginForm.password,
    });
    loginForm.password = '';
    if (!authenticated) throw new Error('登录后未建立有效会话');
    await finishLogin();
  } catch (error) {
    loginForm.password = '';
    pageError.value = authErrorMessage(error, '登录失败，请稍后再试');
  } finally {
    loginLoading.value = false;
  }
};

const handleBootstrap = async () => {
  if (bootstrapLoading.value) return;
  pageError.value = '';
  bootstrapValidationVisible.value = true;
  if (Object.keys(bootstrapValidationErrors.value).length > 0) {
    pageError.value = '请按字段提示修正首次管理员信息';
    return;
  }
  bootstrapLoading.value = true;
  try {
    const authenticated = await authStore.bootstrap({
      bootstrap_token: bootstrapForm.bootstrap_token,
      username: bootstrapForm.username.trim(),
      display_name: bootstrapForm.display_name.trim(),
      password: bootstrapForm.password,
    });
    bootstrapValidationVisible.value = false;
    bootstrapForm.bootstrap_token = '';
    bootstrapForm.password = '';
    bootstrapPasswordConfirmation.value = '';
    if (!authenticated) throw new Error('初始化后未建立有效会话');
    await finishLogin();
  } catch (error) {
    bootstrapValidationVisible.value = false;
    bootstrapForm.bootstrap_token = '';
    bootstrapForm.password = '';
    bootstrapPasswordConfirmation.value = '';
    pageError.value = bootstrapErrorMessage(error, '首次管理员创建失败，请稍后再试');
    if (axios.isAxiosError(error) && error.response?.status === 409) {
      await authStore.loadOptions().catch(() => undefined);
    }
  } finally {
    bootstrapLoading.value = false;
  }
};

onMounted(async () => {
  optionsLoading.value = true;
  try {
    await authStore.loadOptions();
  } catch {
    pageError.value = '无法加载登录配置，请稍后刷新页面';
  } finally {
    optionsLoading.value = false;
  }
});
</script>

<style scoped>
.login-container {
  min-height: 100vh;
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 24px;
  box-sizing: border-box;
  background-color: #f5f7fa;
}

.login-card {
  width: min(440px, 100%);
}

.login-heading {
  text-align: center;
}

.login-heading h2 {
  margin: 0;
  color: #303133;
}

.login-heading p {
  margin: 8px 0 0;
  color: #909399;
  font-size: 13px;
}

.login-form,
.section-gap {
  margin-bottom: 16px;
}

.full-width {
  width: 100%;
}

.bootstrap-field {
  width: 100%;
}

.field-hint {
  display: block;
  margin-top: 4px;
  color: #909399;
  font-size: 12px;
  line-height: 1.5;
}

.field-error {
  display: block;
  margin-top: 4px;
  color: #f56c6c;
  font-size: 12px;
  line-height: 1.5;
}
</style>
