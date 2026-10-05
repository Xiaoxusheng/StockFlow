import { BarcodeView } from './BarcodeView'
import { QrCodeView } from './QrCodeView'
import { LABEL_CELL_MM, labelGridCapacity, labelQrLevel, labelQrSizeMm, mmToPx } from './labelSheet'
import { tryBuildSfqrSku } from '@/utils/qrPayload'
import {
  isLabelObjectType,
  PRINT_LINE_FIELD_LABELS,
  PRINT_LINE_FIELD_ORDER,
  resolveLineFieldLabel,
  type PrintContentRow,
  type PrintTemplateSnapshot,
} from '@/api/printing'

export interface PrintContentRendererProps {
  /** 任务冻结的模板快照（printing.md §2：字段绑定/条码/二维码/页眉） */
  template: PrintTemplateSnapshot
  row: PrintContentRow
  /** 页脚「打印时间」（调用方传入任务时间或当前时间） */
  printedAt?: string
  /** 页脚「制表人」（打印任务创建人） */
  printedBy?: string
}

/** 标签类字号按纸张自适应：热敏 40×30 用小字号，其余常规 */
function labelFontSize(paperKey: string | undefined): number {
  return paperKey === 'THERMAL_40_30' ? 10 : 13
}

/** 模板绑定字段取值（快照 fields 为「绑定键 → 文案」映射，
 * internal/printing/models.go TemplateSnapshot.Fields；键序由后端快照承载，仅取有值项） */
function resolveFieldEntries(
  template: PrintTemplateSnapshot,
  row: PrintContentRow,
): Array<{ label: string; value: string }> {
  return Object.entries(template.fields ?? {})
    .map(([key, label]) => ({ label, value: row.values?.[key] }))
    .filter((entry): entry is { label: string; value: string } => Boolean(entry.value))
}

/** 标签 QR 内容（printing.md §6 定案注记、qr-code.md §7.3）：
 * SKU_LABEL 且 qrcode_enabled → SFQR 载荷（由 values.sku_code 构造，后端装配恒产出该键）；
 * 旧任务快照无 sku_code 值 → 维持主条码原文渲染（扫码走条码匹配器，行为不变）；
 * 其他标签类（库位/箱码/托盘）→ 主码原文（既有口径）。构造唯一点 = utils/qrPayload.ts。 */
function resolveLabelQrValue(template: PrintTemplateSnapshot, row: PrintContentRow): string | null {
  if (!template.qrcode_enabled) return null
  if (template.object_type !== 'SKU_LABEL') return row.code
  return tryBuildSfqrSku(row.values?.sku_code) ?? row.code
}

/** SKU 编码展示文本：新任务 values 恒含 sku_code（printing.md §6 装配保证）；
 * 旧任务快照缺失时兜底主码原文，不造数据 */
function resolveSkuCodeText(row: PrintContentRow): string {
  return row.values?.sku_code || row.code
}

/** QR 渲染参数：尺寸按纸张 mm 计算（qr-code.md §7.3 表格）、纠错按尺寸升降（<24mm 升 Q） */
function labelQrRender(paperKey: string | undefined): { sizePx: number; level: 'M' | 'Q' } {
  const sizeMm = labelQrSizeMm(paperKey)
  return { sizePx: mmToPx(sizeMm), level: labelQrLevel(sizeMm) }
}

/** 极简布局（qr-code.md §7.3：热敏 40×30 只放 QR + SKU 编码 + 商品名（超长省略号截断），
 * 不渲染一维条码与其它字段）。横向排布给 20mm QR 与两行文字留足 40×30 物理空间。 */
function MinimalSkuLabel({
  row,
  qrValue,
  qrSizePx,
  qrLevel,
}: {
  row: PrintContentRow
  qrValue: string | null
  qrSizePx: number
  qrLevel: 'M' | 'Q'
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 4, width: '100%', overflow: 'hidden' }}>
      {qrValue && <QrCodeView value={qrValue} size={qrSizePx} level={qrLevel} variant="print" />}
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 2 }}>
        <div style={{ fontSize: 10, fontWeight: 700, wordBreak: 'break-all', lineHeight: 1.2 }}>
          {resolveSkuCodeText(row)}
        </div>
        <div
          style={{
            fontSize: 9,
            lineHeight: 1.2,
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
          title={row.values?.product_name}
        >
          {row.values?.product_name ?? ''}
        </div>
      </div>
    </div>
  )
}

/** SKU 标签标准布局（热敏 60×40 默认推荐 / 100×50，qr-code.md §7.3）：QR 与文字区横排、
 * 一维主条码全宽置底——同一标签 QR（SFQR 协议身份）与一维条码（物流扫码存量习惯）双身份并存
 * （qr-code.md §2.5）。条码不显示文本：上方主码即人读身份，省出毫米给码区。 */
function StandardSkuLabel({
  template,
  row,
  fieldEntries,
  fontSize,
  qrValue,
  qrSizePx,
  qrLevel,
}: {
  template: PrintTemplateSnapshot
  row: PrintContentRow
  fieldEntries: Array<{ label: string; value: string }>
  fontSize: number
  qrValue: string | null
  qrSizePx: number
  qrLevel: 'M' | 'Q'
}) {
  const wide = template.paper === 'THERMAL_100_50'
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2, width: '100%', overflow: 'hidden' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6 }}>
        {qrValue && <QrCodeView value={qrValue} size={qrSizePx} level={qrLevel} variant="print" />}
        <div style={{ flex: 1, minWidth: 0, textAlign: 'left' }}>
          <div style={{ fontSize, fontWeight: 700, wordBreak: 'break-all', lineHeight: 1.25 }}>{row.code}</div>
          {fieldEntries.map((entry) => (
            <div key={entry.label} style={{ fontSize: fontSize - 2, lineHeight: 1.4 }}>
              {entry.label}：{entry.value}
            </div>
          ))}
        </div>
      </div>
      <BarcodeView
        value={row.code}
        format={template.barcode_symbology ?? 'CODE128'}
        moduleWidth={wide ? 1.5 : 1}
        height={wide ? 28 : 20}
        displayValue={false}
      />
    </div>
  )
}

/** 其他标签类（库位/箱码/托盘）布局：维持既有纵向结构（主码 + 条码/二维码并排 + 字段行），
 * QR 内容为主码原文、尺寸维持既有两档——本轮纸张适配只落在 SKU 标签域（qr-code.md §7.3） */
function GenericLabelLayout({
  template,
  row,
  fieldEntries,
  fontSize,
}: {
  template: PrintTemplateSnapshot
  row: PrintContentRow
  fieldEntries: Array<{ label: string; value: string }>
  fontSize: number
}) {
  return (
    <div style={{ textAlign: 'center' }}>
      <div style={{ fontSize: fontSize + 3, fontWeight: 700, wordBreak: 'break-all' }}>{row.code}</div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6, margin: '6px 0' }}>
        <BarcodeView
          value={row.code}
          format={template.barcode_symbology ?? 'CODE128'}
          height={template.paper === 'THERMAL_40_30' ? 28 : 44}
          fontSize={fontSize - 1}
        />
        {template.qrcode_enabled && <QrCodeView value={row.code} size={template.paper === 'THERMAL_40_30' ? 48 : 72} />}
      </div>
      {fieldEntries.map((entry) => (
        <div key={entry.label} style={{ fontSize, lineHeight: 1.5, textAlign: 'left' }}>
          {entry.label}：{entry.value}
        </div>
      ))}
    </div>
  )
}

/** 标签布局分发（printing.md §6 纸张适配注记） */
function LabelLayout({ template, row }: Required<Pick<PrintContentRendererProps, 'template' | 'row'>>) {
  const paperKey = template.paper
  const fontSize = labelFontSize(paperKey)
  const fieldEntries = resolveFieldEntries(template, row)
  const qrValue = resolveLabelQrValue(template, row)
  const { sizePx: qrSizePx, level: qrLevel } = labelQrRender(paperKey)

  if (template.object_type === 'SKU_LABEL') {
    if (paperKey === 'THERMAL_40_30') {
      return <MinimalSkuLabel row={row} qrValue={qrValue} qrSizePx={qrSizePx} qrLevel={qrLevel} />
    }
    return (
      <StandardSkuLabel
        template={template}
        row={row}
        fieldEntries={fieldEntries}
        fontSize={fontSize}
        qrValue={qrValue}
        qrSizePx={qrSizePx}
        qrLevel={qrLevel}
      />
    )
  }
  return <GenericLabelLayout template={template} row={row} fieldEntries={fieldEntries} fontSize={fontSize} />
}

/** 单据明细列：行数据实际键按预设顺序排列（预设外的键殿后），文案取统一映射 */
function resolveLineColumns(lines: Array<Record<string, string>>): string[] {
  const keys: string[] = []
  for (const key of PRINT_LINE_FIELD_ORDER) {
    if (lines.some((line) => line[key] !== undefined)) keys.push(key)
  }
  for (const key of Object.keys(PRINT_LINE_FIELD_LABELS)) {
    if (!keys.includes(key) && lines.some((line) => line[key] !== undefined)) keys.push(key)
  }
  for (const key of lines[0] ? Object.keys(lines[0]) : []) {
    if (!keys.includes(key)) keys.push(key)
  }
  return keys
}

/** 单据布局：页眉（公司名/单据名）+ 表头信息 + 明细表 + 页脚（printing.md §2 单据类模板）。
 * 快照 qrcode_enabled 时在页眉右上渲染单据二维码（printing.md §4.2 可生成的码）：
 * 内容为单据号（row.code），与后端渲染管线逐行预生成同源（service_task.go:397-402），
 * 扫码经 /api/scanner/resolve 单号前缀直达单据详情（printing.md §4.3） */
function DocumentLayout({ template, row, printedAt, printedBy }: PrintContentRendererProps) {
  const lines = row.lines ?? []
  const columns = resolveLineColumns(lines)
  const headerText = template.header_text ?? template.name

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ position: 'relative', marginBottom: 10 }}>
        <div style={{ textAlign: 'center', fontWeight: 700, fontSize: 16 }}>{headerText}</div>
        {template.qrcode_enabled && (
          <div style={{ position: 'absolute', top: 0, right: 0, lineHeight: 0 }}>
            <QrCodeView value={row.code} size={64} />
          </div>
        )}
      </div>
      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: '2px 24px',
          fontSize: 12,
          marginBottom: 8,
        }}
      >
        <span>单据号：{row.code}</span>
        {Object.entries(template.fields ?? {}).map(([key, label]) => (
          <span key={key}>
            {label}：{row.values?.[key] ?? '-'}
          </span>
        ))}
      </div>
      <table
        style={{
          width: '100%',
          borderCollapse: 'collapse',
          fontSize: 12,
          tableLayout: 'fixed',
        }}
      >
        <thead>
          <tr>
            {columns.map((column) => (
              <th
                key={column}
                style={{ border: '1px solid #000000', padding: '3px 4px', fontWeight: 600, textAlign: 'left' }}
              >
                {resolveLineFieldLabel(column)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {lines.map((line, index) => (
            <tr key={index}>
              {columns.map((column) => (
                <td key={column} style={{ border: '1px solid #000000', padding: '3px 4px', wordBreak: 'break-all' }}>
                  {line[column] ?? ''}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <div style={{ marginTop: 'auto', paddingTop: 10, fontSize: 12, display: 'flex', justifyContent: 'space-between' }}>
        <span>制表人：{printedBy ?? row.values?.operator ?? '-'}</span>
        <span>审核人：{row.values?.reviewer ?? '-'}</span>
        <span>打印时间：{printedAt ?? '-'}</span>
      </div>
    </div>
  )
}

export interface PrintLabelGridProps {
  /** 任务冻结的模板快照 */
  template: PrintTemplateSnapshot
  /** 本片内容行（不超过 labelGridCapacity(paper).perSheet，分片见 labelSheet.chunkLabelRows） */
  rows: PrintContentRow[]
}

/** A4/A5 网格分片渲染（qr-code.md §7.3）：CSS grid 固定 50×30mm 列宽/行高、单元 break-inside:avoid，
 * 分页不截断标签。单元内紧凑布局：QR（22mm、升 Q 纠错）+ SKU 编码 + 其余绑定字段小字；
 * 一维条码在 50×30mm 单元内低于可靠扫码密度故不渲染（需要一维码请打印热敏纸规格）。 */
export function PrintLabelGrid({ template, rows }: PrintLabelGridProps) {
  const capacity = labelGridCapacity(template.paper)
  const qrSizeMm = labelQrSizeMm(template.paper)
  const qrSizePx = mmToPx(qrSizeMm)
  const qrLevel = labelQrLevel(qrSizeMm)

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: `repeat(${capacity.cols}, ${LABEL_CELL_MM.widthMm}mm)`,
        gridAutoRows: `${LABEL_CELL_MM.heightMm}mm`,
      }}
    >
      {rows.map((row) => {
        const qrValue = resolveLabelQrValue(template, row)
        const skuCodeText = resolveSkuCodeText(row)
        // SKU 编码已占主位加粗展示，绑定字段区不再重复渲染该键
        const otherEntries = resolveFieldEntries(template, row).filter((entry) => entry.value !== skuCodeText)
        return (
          <div
            key={row.id}
            style={{
              breakInside: 'avoid',
              pageBreakInside: 'avoid',
              boxSizing: 'border-box',
              padding: '2mm',
              overflow: 'hidden',
              display: 'flex',
              alignItems: 'center',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 4, width: '100%', overflow: 'hidden' }}>
              {qrValue && <QrCodeView value={qrValue} size={qrSizePx} level={qrLevel} variant="print" />}
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 10, fontWeight: 700, wordBreak: 'break-all', lineHeight: 1.2 }}>
                  {skuCodeText}
                </div>
                {otherEntries.map((entry) => (
                  <div
                    key={entry.label}
                    style={{
                      fontSize: 8,
                      lineHeight: 1.3,
                      whiteSpace: 'nowrap',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                    }}
                    title={entry.value}
                  >
                    {entry.label}：{entry.value}
                  </div>
                ))}
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}

/**
 * 模板内容渲染器：一行内容 = 一张纸（标签一码一页；单据一单一张，超长明细由浏览器
 * 打印分页自然续页）。纯展示组件，数据全部来自打印任务的真实内容行。
 * A4/A5 标签网格分片不经本组件逐行进入——由 PrintLabelGrid 按片渲染（PrintPreviewPage 编排）。
 */
export function PrintContentRenderer(props: PrintContentRendererProps) {
  const { template, row } = props
  return isLabelObjectType(template.object_type) ? <LabelLayout template={template} row={row} /> : <DocumentLayout {...props} />
}
