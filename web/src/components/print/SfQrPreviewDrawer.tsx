import { useState } from 'react'
import { Button, Descriptions, Drawer, Space, Typography } from 'antd'
import { QrCodeView } from '@/components/print/QrCodeView'
import { SfQrPrintModal, type SfQrPrintSku } from '@/components/print/SfQrPrintModal'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { tryBuildSfqrSku } from '@/utils/qrPayload'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'

const { Paragraph, Text } = Typography

/** 二维码详情抽屉展示对象（调用方从 SKU 行组装；后端 SKU 列表不联表下发商品名） */
export interface SfQrSkuInfo {
  /** SKU 数字 ID（透传打印弹窗，data_ids 通道纪律见 SfQrPrintModal） */
  id: number | string
  /** SKU 编码（SFQR 载荷身份段，qr-code.md §2.1） */
  code: string
  /** 商品名（调用方经 options map 传入） */
  productName?: string
  /** 主条码（is_primary 优先，调用方决议后传入；一维=物流存量习惯，QR=协议身份） */
  primaryBarcode?: string
  /** 启用状态（SfStatusTag enabled/disabled） */
  enabled?: boolean
}

export interface SfQrPreviewDrawerProps {
  open: boolean
  sku: SfQrSkuInfo | null
  onClose: () => void
}

/**
 * 二维码详情抽屉（frontend.md §13.1 冻结；SKU 列表/二维码中心共用「详情快捷入口」）：
 * QrCodeView 约 180px 预览（克制尺寸，约束 8 禁止巨大二维码）+ 载荷明文（复制自证，qr-code.md §7.2）
 * + SKU 编码/商品名/主条码/状态（SfStatusTag）+「打印标签」按钮（printing:task:create fail-closed）。
 *
 * 载荷经全站构造唯一点 tryBuildSfqrSku 动态生成，不持久化（约束 5）；
 * 主条码与 SFQR 双身份分工见 qr-code.md §2.5。主操作按钮触控目标 ≥44px（约束 8 Pad 适配）。
 */
export function SfQrPreviewDrawer({ open, sku, onClose }: SfQrPreviewDrawerProps) {
  const user = useAuthStore((s) => s.user)
  const [printOpen, setPrintOpen] = useState(false)

  const payload = sku ? tryBuildSfqrSku(sku.code) : null

  return (
    <>
      <Drawer
        title="二维码详情"
        open={open}
        size={420}
        onClose={onClose}
        destroyOnHidden
      >
        {sku && (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            {/* 预览约 180px（frontend.md §13.1 冻结尺寸）；值空/构造失败由 QrCodeView 统一降级 */}
            <div style={{ textAlign: 'center', padding: '12px 0' }}>
              <QrCodeView value={payload ?? ''} size={180} />
            </div>

            {/* 载荷明文随预览展示 + 复制（qr-code.md §1 运行时自证层，供人工核对协议正确性） */}
            {payload && (
              <Paragraph copyable={{ text: payload, tooltips: ['复制载荷', '已复制'] }} style={{ marginBottom: 0 }}>
                {payload}
              </Paragraph>
            )}

            <Descriptions
              size="small"
              column={1}
              bordered
              items={[
                { key: 'code', label: 'SKU 编码', children: <Text copyable={{ text: sku.code }}>{sku.code}</Text> },
                { key: 'product', label: '商品名', children: sku.productName || '-' },
                { key: 'barcode', label: '主条码', children: sku.primaryBarcode || '-' },
                {
                  key: 'status',
                  label: '状态',
                  children: <SfStatusTag status={sku.enabled === false ? 'disabled' : 'enabled'} />,
                },
              ]}
            />

            <Text type="secondary" style={{ fontSize: 12 }}>
              二维码由 SKU 编码按 SFQR 协议（v1）动态生成，不持久化；扫码枪扫入经
              POST /api/scanner/resolve 识别后直达本页。一维条码为物流扫码身份，二维码为协议身份，
              两者并存（qr-code.md §2.5）。
            </Text>

            {/* 打印入口 fail-closed（frontend.md §13.1：canAccess('printing:task:create')）；
                触控目标 ≥44px（约束 8） */}
            {canAccess(user, 'printing:task:create') && (
              <Button
                type="primary"
                size="large"
                block
                style={{ minHeight: 44 }}
                disabled={!payload}
                onClick={() => setPrintOpen(true)}
              >
                打印标签
              </Button>
            )}
          </Space>
        )}
      </Drawer>

      {/* 单打配置弹窗（sku 存在才装配，open 受 printOpen 控制） */}
      {sku && (
        <SfQrPrintModal
          open={printOpen}
          skus={
            [
              { id: sku.id, code: sku.code, productName: sku.productName, enabled: sku.enabled },
            ] satisfies SfQrPrintSku[]
          }
          onClose={() => setPrintOpen(false)}
        />
      )}
    </>
  )
}
