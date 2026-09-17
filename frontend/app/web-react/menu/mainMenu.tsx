import type { ReactNode } from 'react';
import {
  IconApps,
  IconEdit,
  IconHome,
  IconInfoCircle,
  IconLineChartStroked,
  IconLink,
  IconList,
  IconSetting,
  IconTerminal,
  IconUpload,
  IconUserCircle,
  IconWrench,
} from '@douyinfe/semi-icons';
import type { NavItems } from '@douyinfe/semi-ui-19/lib/es/navigation';
import { PERMISSIONS } from '@shared/types/auth';
import type { Permission } from '@shared/types/auth';
import { isMigrated } from '../routes/migration';

interface MenuNode {
  itemKey: string;
  text: string;
  icon: ReactNode;
  /** Node is visible only when the identity holds at least one of these. */
  anyOf?: Permission[];
  items?: MenuNode[];
}

const MENU: MenuNode[] = [
  { itemKey: '/', text: '首页', icon: <IconHome /> },
  {
    itemKey: '/application',
    text: '应用管理',
    icon: <IconApps />,
    anyOf: [PERMISSIONS.APPLICATIONS_READ, PERMISSIONS.APPLICATIONS_WRITE],
    items: [
      {
        itemKey: '/application/list',
        text: '应用列表',
        icon: <IconList />,
        anyOf: [PERMISSIONS.APPLICATIONS_READ],
      },
      {
        itemKey: '/application/apply',
        text: '应用申请',
        icon: <IconEdit />,
        anyOf: [PERMISSIONS.APPLICATIONS_WRITE],
      },
    ],
  },
  {
    itemKey: '/publish',
    text: '发布工具',
    icon: <IconUpload />,
    anyOf: [PERMISSIONS.RELEASES_READ, PERMISSIONS.RELEASES_CREATE],
    items: [
      {
        itemKey: '/publish/deploy',
        text: '服务发布',
        icon: <IconUpload />,
        anyOf: [PERMISSIONS.RELEASES_READ],
      },
      {
        itemKey: '/publish/merge',
        text: '代码合并',
        icon: <IconLink />,
        anyOf: [PERMISSIONS.RELEASES_CREATE],
      },
    ],
  },
  {
    itemKey: '/operation',
    text: '运维管理',
    icon: <IconWrench />,
    anyOf: [PERMISSIONS.LOGS_READ, PERMISSIONS.RELEASES_CREATE, PERMISSIONS.KUBERNETES_READ],
    items: [
      {
        itemKey: '/operation/log',
        text: '日志查询',
        icon: <IconTerminal />,
        anyOf: [PERMISSIONS.LOGS_READ],
      },
      {
        itemKey: '/operation/monitor',
        text: '监控面板',
        icon: <IconLineChartStroked />,
        anyOf: [PERMISSIONS.KUBERNETES_READ],
      },
    ],
  },
  {
    itemKey: '/system',
    text: '系统设置',
    icon: <IconSetting />,
    items: [
      {
        itemKey: '/system/settings',
        text: '系统配置',
        icon: <IconWrench />,
        anyOf: [PERMISSIONS.SYSTEM_SETTINGS_READ],
      },
      {
        itemKey: '/system/users',
        text: '用户与角色',
        icon: <IconUserCircle />,
        anyOf: [PERMISSIONS.USERS_READ],
      },
      { itemKey: '/system/version', text: '版本信息', icon: <IconInfoCircle /> },
    ],
  },
];

/**
 * Drops nodes the identity cannot see and parents left without a visible child,
 * mirroring the Vue template's per-item `v-if` plus its parent `v-if` guards.
 * Exported so the permission parity test can assert the structure directly.
 */
export const visibleMenuItems = (
  canAny: (required: Permission[]) => boolean,
  nodes: MenuNode[] = MENU
): MenuNode[] => {
  const result: MenuNode[] = [];
  for (const node of nodes) {
    if (node.anyOf && !canAny(node.anyOf)) continue;
    if (node.items) {
      const children = visibleMenuItems(canAny, node.items);
      if (children.length === 0) continue;
      result.push({ ...node, items: children });
      continue;
    }
    result.push(node);
  }
  return result;
};

export const toNavItems = (nodes: MenuNode[]): NavItems =>
  nodes.map(node => ({
    itemKey: node.itemKey,
    text: node.text,
    icon: node.icon,
    // Category nodes only expand/collapse. Disabling them would also make a
    // newly migrated child unreachable unless every ancestor were duplicated
    // in the route set.
    disabled: !node.items && !isMigrated(node.itemKey),
    items: node.items ? toNavItems(node.items) : undefined,
  }));
