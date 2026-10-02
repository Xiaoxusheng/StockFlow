import { Button, Result, Flex } from 'antd'
import { useNavigate } from 'react-router'

export interface ErrorPageProps {
  status: '403' | '404' | '500'
  title: string
  description?: string
}

/** 统一错误页（frontend.md §9.4：403 / 404 / 500） */
export function ErrorPage({ status, title, description }: ErrorPageProps) {
  const navigate = useNavigate()
  return (
    <Flex
      align="center"
      justify="center"
      style={{ minHeight: '100vh', background: 'var(--sf-bg)' }}
    >
      <Result
        status={status}
        title={title}
        subTitle={description}
        extra={
          <Flex gap={8} justify="center">
            <Button type="primary" onClick={() => navigate('/dashboard')}>
              返回首页
            </Button>
            <Button onClick={() => navigate(-1)}>返回上一页</Button>
          </Flex>
        }
      />
    </Flex>
  )
}
