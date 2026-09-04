import axios from 'axios';
import api from '@/config/api';
import type {
  ApiEnvelope,
  ManagedUser,
  ManagedUserList,
  UpdateManagedUserRequest,
} from '@/types/auth';

const USERS_BASE_URL = '/api/v1/system/users';

const unwrap = <T>(response: ApiEnvelope<T>): T => {
  if (response.code !== 1) {
    const details = typeof response.error === 'string' ? response.error : '';
    throw new Error(details || response.message || '请求失败');
  }
  return response.result;
};

export const listUsers = async (offset = 0, limit = 100): Promise<ManagedUserList> => {
  const response = await api.get<ApiEnvelope<ManagedUserList>>(USERS_BASE_URL, {
    params: { offset, limit },
  });
  return unwrap(response.data);
};

export const updateUser = async (
  userID: string,
  request: UpdateManagedUserRequest
): Promise<ManagedUser> => {
  const response = await api.patch<ApiEnvelope<ManagedUser>>(
    `${USERS_BASE_URL}/${encodeURIComponent(userID)}`,
    request
  );
  return unwrap(response.data);
};

export const getUserApiError = (error: unknown): { status?: number; message: string } => {
  if (axios.isAxiosError<ApiEnvelope<unknown>>(error)) {
    const response = error.response?.data;
    const details = typeof response?.error === 'string' ? response.error : '';
    return {
      status: error.response?.status,
      message: details || response?.message || error.message || '请求失败',
    };
  }
  return { message: error instanceof Error ? error.message : '请求失败' };
};
