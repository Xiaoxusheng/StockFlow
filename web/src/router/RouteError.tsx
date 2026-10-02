import { Result, Button, Flex } from 'antd'
import { useNavigate, useRouteError } from 'react-router'
import { resolveErrorMessage } from '@/api/client'

/** 路由渲染错误兜底：页面崩溃时保持 Layout，展示可读错误 */
export function RouteError() {
  const error = useRouteError()
  const navigate = useNavigate()
  return (
    <div className="sf-page">
      <Flex align="center" justify="center" style={{ minHeight: '50vh' }}>
        <Result
          status="warning"
          title="页面出现异常"
          subTitle={resolveErrorMessage(error)}
          extra={
            <Flex gap={8} justify="center">
              <Button type="primary" onClick={() => window.location.reload()}>
                刷新页面
              </Button>
              <Button onClick={() => navigate('/dashboard')}>返回首页</Button>
            </Flex>
          }
        />
      </Flex>
    </div>
  )
}
