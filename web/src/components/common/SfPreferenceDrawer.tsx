import { Drawer, Form, Select, Space, Button, Popconfirm, Typography, message } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { fetchWarehouseOptions, idKey } from '@/api/options'
import { usePreferenceValue, useSetPreference, useClearRecentData } from '@/hooks/usePreferences'

// ---------- 偏好设置抽屉（计划 §2.3 / frontend.md §23 组件清单） ----------
// 入口=PC 用户菜单『偏好设置』项（PcLayout 挂载）；内容=默认仓库（['options','warehouses']
// 既有选项接口）+ 清除最近数据。写经 useSetPreference（本地即时 + 防抖 PUT 白名单 key）。

export function SfPreferenceDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [messageApi, contextHolder] = message.useMessage()
  const setPreference = useSetPreference()
  const clearRecentData = useClearRecentData()
  const defaultWarehouseId = usePreferenceValue<string | null>('default_warehouse_id', null)

  // 默认仓库候选：既有 options 接口（与各列表页筛选下拉同源缓存）
  const warehouses = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
    staleTime: 5 * 60_000,
  })

  const handleWarehouseChange = (value: string | undefined) => {
    // 值为原始 JSON 字符串（后端 PUT body=原始 JSON 值，非 {value:...} 包装）
    setPreference('default_warehouse_id', value ?? null)
    void messageApi.success(value ? '默认仓库已保存' : '默认仓库已清除')
  }

  const handleClearRecent = () => {
    clearRecentData()
    void messageApi.success('最近访问与最近筛选已清除')
  }

  return (
    <Drawer title="偏好设置" open={open} onClose={onClose} width={380}>
      {contextHolder}
      <Form layout="vertical" component={false}>
        <Form.Item label="默认仓库" extra="登录后业务页面的初始仓库筛选">
          <Select
            allowClear
            showSearch
            optionFilterProp="label"
            loading={warehouses.isLoading}
            value={defaultWarehouseId ?? undefined}
            placeholder={warehouses.isLoading ? '加载中…' : '选择默认仓库'}
            options={(warehouses.data ?? []).map((w) => ({
              value: idKey(w.id),
              label: `${w.code} ${w.name}`,
            }))}
            onChange={handleWarehouseChange}
          />
        </Form.Item>
      </Form>
      <Space direction="vertical" size="middle" style={{ width: '100%', marginTop: 'var(--sf-space-4)' }}>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          最近数据仅保存在你的账号下，不影响他人。
        </Typography.Text>
        <Popconfirm
          title="清除最近数据？"
          description="将清空最近访问与最近筛选记录。"
          okText="清除"
          cancelText="取消"
          onConfirm={handleClearRecent}
        >
          <Button danger>清除最近数据</Button>
        </Popconfirm>
      </Space>
    </Drawer>
  )
}
