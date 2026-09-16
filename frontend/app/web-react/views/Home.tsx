import { Card, Typography } from '@douyinfe/semi-ui-19';
import { useAuthStore } from '../stores/auth';

const { Text, Title } = Typography;

/**
 * B0 placeholder home. B1 replaces it with the migrated page; it exists so the
 * authenticated landing route is renderable and the guard can be verified.
 */
export default function Home() {
  const user = useAuthStore(state => state.user);
  return (
    <Card style={{ marginTop: 16 }}>
      <Title heading={5}>Ares 控制台</Title>
      <Text type='tertiary'>
        当前身份：{user?.display_name || user?.username || '未知'}。此页面为 B0 骨架占位，B1
        批次迁移正式首页。
      </Text>
    </Card>
  );
}
