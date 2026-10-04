import { BarcodeView } from './BarcodeView'
import { QrCodeView } from './QrCodeView'
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

/** 标签布局：主码文本 + 条码/二维码 + 已绑定字段行（printing.md §2 库位标签示例） */
function LabelLayout({ template, row }: Required<Pick<PrintContentRendererProps, 'template' | 'row'>>) {
  const fontSize = labelFontSize(template.paper)
  // 快照 fields 为「绑定键 → 文案」映射（TemplateSnapshot.Fields map[string]string，
  // internal/printing/models.go:196-203/224-232；键序由后端快照承载）
  const fieldEntries = Object.entries(template.fields ?? {})
    .map(([key, label]) => ({ label, value: row.values?.[key] }))
    .filter((entry): entry is { label: string; value: string } => Boolean(entry.value))

  return (
    <div style={{ textAlign: 'center' }}>
      <div style={{ fontSize: fontSize + 3, fontWeight: 700, wordBreak: 'break-all' }}>{row.code}</div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6, margin: '6px 0' }}>
        <BarcodeView value={row.code} format={template.barcode_symbology ?? 'CODE128'} height={template.paper === 'THERMAL_40_30' ? 28 : 44} fontSize={fontSize - 1} />
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

/** 单据布局：页眉（公司名/单据名）+ 表头信息 + 明细表 + 页脚（printing.md §2 单据类模板） */
function DocumentLayout({ template, row, printedAt, printedBy }: PrintContentRendererProps) {
  const lines = row.lines ?? []
  const columns = resolveLineColumns(lines)
  const headerText = template.header_text ?? template.name

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ textAlign: 'center', fontWeight: 700, fontSize: 16, marginBottom: 10 }}>{headerText}</div>
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

/**
 * 模板内容渲染器：一行内容 = 一张纸（标签一码一页；单据一单一张，超长明细由浏览器
 * 打印分页自然续页）。纯展示组件，数据全部来自打印任务的真实内容行。
 */
export function PrintContentRenderer(props: PrintContentRendererProps) {
  const { template, row } = props
  return isLabelObjectType(template.object_type) ? <LabelLayout template={template} row={row} /> : <DocumentLayout {...props} />
}
