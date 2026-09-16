import { Layout, Typography } from '@douyinfe/semi-ui-19';
import { Outlet } from 'react-router';

const { Header, Content } = Layout;
const { Title, Text } = Typography;

/**
 * B0 placeholder shell. B1 replaces this with the permission-aware MainLayout
 * that mirrors the Vue navigation, so it deliberately renders no menu yet.
 */
export default function AppShell() {
  return (
    <Layout style={{ height: '100vh' }}>
      <Header style={{ backgroundColor: 'var(--semi-color-bg-1)', paddingLeft: 24 }}>
        <Title heading={5} style={{ lineHeight: '64px', margin: 0 }}>
          Ares
        </Title>
      </Header>
      <Content style={{ padding: 24, overflow: 'auto' }}>
        <Text type='tertiary'>React + Semi 外壳（B0 骨架），页面迁移按批次进行。</Text>
        <Outlet />
      </Content>
    </Layout>
  );
}
