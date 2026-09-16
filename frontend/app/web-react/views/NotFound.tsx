import { Button, Empty } from '@douyinfe/semi-ui-19';
import { useNavigate } from 'react-router';

export default function NotFound() {
  const navigate = useNavigate();
  return (
    <Empty title='页面不存在' description='请检查地址，或返回首页。' style={{ padding: 48 }}>
      <Button theme='solid' type='primary' onClick={() => navigate('/', { replace: true })}>
        返回首页
      </Button>
    </Empty>
  );
}
