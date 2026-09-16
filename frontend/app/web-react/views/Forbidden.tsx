import { Button, Empty } from '@douyinfe/semi-ui-19';
import { useNavigate, useSearchParams } from 'react-router';
import { normalizeReturnTo } from '@shared/utils/return-to';
import { useAuthStore } from '../stores/auth';

export default function Forbidden() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const user = useAuthStore(state => state.user);
  const from = normalizeReturnTo(searchParams.get('from'), '/');

  return (
    <Empty
      title='没有访问该页面的权限'
      description={user ? `当前身份：${user.username}` : undefined}
      style={{ padding: 48 }}
    >
      <Button theme='solid' type='primary' onClick={() => navigate(from, { replace: true })}>
        返回上一位置
      </Button>
    </Empty>
  );
}
