import { Button, Card, Typography } from '@douyinfe/semi-ui-19';
import {
  IconApps,
  IconClock,
  IconLink,
  IconPlus,
  IconTickCircle,
  IconUpload,
} from '@douyinfe/semi-icons';
import { useNavigate } from 'react-router';
import { PERMISSIONS } from '@shared/types/auth';
import { useAuthStore } from '../stores/auth';
import { isMigrated } from '../routes/migration';

const { Title, Text } = Typography;

const STATS = [
  { key: 'applications', label: '应用总数', icon: <IconApps /> },
  { key: 'pending', label: '待发布', icon: <IconClock /> },
  { key: 'released', label: '已发布', icon: <IconTickCircle /> },
];

const QUICK_ACTIONS = [
  {
    path: '/application/apply',
    label: '申请应用',
    icon: <IconPlus />,
    permission: PERMISSIONS.APPLICATIONS_WRITE,
  },
  {
    path: '/publish/merge',
    label: '代码合并',
    icon: <IconLink />,
    permission: PERMISSIONS.RELEASES_CREATE,
  },
  {
    path: '/publish/deploy',
    label: '服务发布',
    icon: <IconUpload />,
    permission: PERMISSIONS.RELEASES_READ,
  },
];

export default function Home() {
  const navigate = useNavigate();
  const permissions = useAuthStore(state => state.user?.permissions) || [];
  const can = (permission: string) => (permissions as string[]).includes(permission);

  return (
    <div style={{ display: 'flex', gap: 20, flexWrap: 'wrap' }}>
      <Card style={{ flex: '1 1 280px', minHeight: 280 }} title='欢迎使用'>
        <div style={{ textAlign: 'center', paddingTop: 32 }}>
          <Title heading={3} className='welcome-title'>
            Ares
          </Title>
          <Text type='tertiary'>一站式应用发布管理平台</Text>
        </div>
      </Card>

      <Card style={{ flex: '1 1 280px', minHeight: 280 }} title='应用统计'>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18, paddingTop: 16 }}>
          {STATS.map(stat => (
            <div
              key={stat.key}
              style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
            >
              <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                {stat.icon}
                {stat.label}
              </span>
              {/* The Vue page renders the same placeholder counters; no API backs them yet. */}
              <Text strong>0</Text>
            </div>
          ))}
        </div>
      </Card>

      <Card style={{ flex: '1 1 280px', minHeight: 280 }} title='快捷操作'>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, paddingTop: 16 }}>
          {QUICK_ACTIONS.filter(action => can(action.permission)).map(action => (
            <Button
              key={action.path}
              theme='light'
              icon={action.icon}
              disabled={!isMigrated(action.path)}
              onClick={() => navigate(action.path)}
            >
              {action.label}
            </Button>
          ))}
        </div>
      </Card>
    </div>
  );
}
