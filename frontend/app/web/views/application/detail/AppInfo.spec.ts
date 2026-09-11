import { flushPromises, mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { describe, expect, it, vi } from 'vitest';
import AppInfo from './AppInfo.vue';

const api = vi.hoisted(() => ({ getAppDetail: vi.fn(), patchApp: vi.fn() }));
vi.mock('@/services/application', () => api);
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { appId: '1' } }) }));
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ can: () => true }) }));

describe('应用基本信息', () => {
  it('展示和编辑均无 Rundeck 字段，正常编辑只提交实际变更', async () => {
    api.getAppDetail.mockResolvedValue({
      data: {
        code: 1,
        result: {
          app_id: 1,
          app_name: 'demo-app',
          app_name_cn: '示例应用',
          owner: 'san.zhang',
          owner_cn: '张三',
          dev_language: 'java',
          git_url: 'git@github.com:go-ree/ares.git',
          description_cn: '示例',
          // 旧服务端的历史字段也不能显示或重新提交。
          rundeck_app_name: 'legacy-alias',
        },
      },
    });
    api.patchApp.mockResolvedValue({ data: { code: 1 } });
    const wrapper = mount(AppInfo, { global: { plugins: [ElementPlus] } });
    try {
      await flushPromises();
      expect(wrapper.text()).toContain('demo-app');
      expect(wrapper.text()).not.toMatch(/Rundeck|legacy-alias/i);
      await wrapper
        .findAll('button')
        .find(button => button.text() === '编辑')!
        .trigger('click');
      expect(wrapper.text()).not.toMatch(/Rundeck|legacy-alias/i);
      expect(wrapper.find('input[placeholder="demo-app"]').exists()).toBe(false);
      await wrapper.get('input[placeholder="中文名"]').setValue('新名称');
      await wrapper
        .findAll('button')
        .find(button => button.text() === '保存')!
        .trigger('click');
      await flushPromises();
      expect(api.patchApp).toHaveBeenCalledWith(1, { app_name_cn: '新名称' });
    } finally {
      wrapper.unmount();
    }
  });
});
