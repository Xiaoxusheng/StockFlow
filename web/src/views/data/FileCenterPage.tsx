import { useState } from 'react'
import {
  Button,
  Card,
  Flex,
  Form,
  Image,
  Input,
  Modal,
  Select,
  theme,
  Typography,
  message,
} from 'antd'
import { DownloadOutlined, EyeOutlined, FileOutlined, UploadOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  FILE_MODULE_OPTIONS,
  FILE_UPLOAD_ACCEPT,
  fetchFileObjectUrl,
  fileApi,
  resolveFileDownloadPath,
  type FileItem,
  type FileQuery,
} from '@/api/file'
import { downloadFile, resolveModuleLabel } from '@/api/data'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfConfirm } from '@/components/common/SfConfirm'
import {
  SfAttachment,
  isImageAttachment,
  type AttachmentItem,
} from '@/components/common/SfAttachment'
import { SfError } from '@/components/common/SfError'
import { formatDateTime, formatFileSize } from '@/utils/format'

const { Text } = Typography

/** 上传表单值（excel.md §7：文件登记业务模块 + 关联单据） */
interface FileUploadFormValues {
  module: string
  businessNo?: string
}

/** 待上传文件唯一 key（同一会话内去重用） */
function pendingFileKey(file: File, index: number): string {
  return `${file.name}-${file.size}-${file.lastModified}-${index}`
}

/**
 * 文件中心（/data/files，excel.md §7 + frontend.md §15.4）：
 * 全平台附件/产物文件统一登记（文件名/类型/大小/上传人/上传时间/业务模块/关联单据），
 * 支持认证下载（downloadFile）、图片预览/全屏（认证流受控 Image）、删除（SfConfirm）与
 * multipart 上传（SfAttachment 附件块作为上传入口）。后端未交付时呈统一错误态，
 * 前端不生成假文件。
 */
export default function FileCenterPage() {
  const { token } = theme.useToken()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const [params, setParams] = useState<FileQuery>({})
  const [uploadOpen, setUploadOpen] = useState(false)
  const [pendingFiles, setPendingFiles] = useState<File[]>([])
  const [downloadingId, setDownloadingId] = useState<string | null>(null)
  const [previewingId, setPreviewingId] = useState<string | null>(null)
  const [preview, setPreview] = useState<{ open: boolean; src: string }>({ open: false, src: '' })
  const [form] = Form.useForm<FileUploadFormValues>()

  const list = usePagedList<FileItem, FileQuery>({
    queryKey: ['data', 'files'],
    fetch: (query) => fileApi.list(query),
    params,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['data', 'files'] })
  }

  /** 上传：逐个文件走真实 multipart 端点（excel.md §7 登记模块/单据），失败即停并呈可读错误 */
  const uploadMutation = useMutation({
    mutationFn: async (values: FileUploadFormValues) => {
      for (const file of pendingFiles) {
        await fileApi.upload({ file, module: values.module, businessNo: values.businessNo })
      }
    },
    onSuccess: () => {
      messageApi.success(`已上传 ${pendingFiles.length} 个文件`)
      setUploadOpen(false)
      setPendingFiles([])
      form.resetFields()
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const removeMutation = useMutation({
    mutationFn: (id: FileItem['id']) => fileApi.remove(id),
    onSuccess: () => {
      messageApi.success('文件已删除')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  /** 下载：复用 downloadFile 认证下载（Blob 落地触发保存），失败呈可读错误态，不生成假文件 */
  const handleDownload = async (record: FileItem) => {
    setDownloadingId(record.id)
    try {
      await downloadFile(resolveFileDownloadPath(record), record.fileName)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setDownloadingId(null)
    }
  }

  /** 预览（图片类型）：认证获取文件流转 objectUrl，交受控 antd Image 预览/全屏 */
  const handlePreview = async (record: FileItem) => {
    setPreviewingId(record.id)
    try {
      const url = await fetchFileObjectUrl(resolveFileDownloadPath(record))
      setPreview({ open: true, src: url })
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setPreviewingId(null)
    }
  }

  const closePreview = (open: boolean) => {
    if (!open) {
      if (preview.src) URL.revokeObjectURL(preview.src)
      setPreview({ open: false, src: '' })
    }
  }

  const openUploadModal = () => {
    setPendingFiles([])
    form.resetFields()
    setUploadOpen(true)
  }

  const handleSubmitUpload = () => {
    form
      .validateFields()
      .then((values) => uploadMutation.mutate(values))
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  /** 待上传文件 → 附件块条目（用户真实所选文件，非造数） */
  const pendingItems: AttachmentItem[] = pendingFiles.map((file, index) => ({
    id: pendingFileKey(file, index),
    fileName: file.name,
    fileType: file.type || undefined,
    size: file.size,
  }))

  const columns: ColumnsType<FileItem> = [
    {
      title: '文件名',
      dataIndex: 'fileName',
      width: 260,
      fixed: 'left',
      render: (value: string, record: FileItem) => (
        <Flex align="center" gap={8}>
          {isImageAttachment(record) && record.thumbnailUrl ? (
            <Image
              width={32}
              height={32}
              src={record.thumbnailUrl}
              preview={false}
              style={{ objectFit: 'cover', borderRadius: 4 }}
            />
          ) : (
            <FileOutlined style={{ fontSize: 18, color: token.colorTextSecondary }} />
          )}
          <Text style={{ maxWidth: 190 }} ellipsis={{ tooltip: value }}>
            {value}
          </Text>
        </Flex>
      ),
    },
    { title: '文件类型', dataIndex: 'fileType', width: 110, render: (v?: string) => v ?? '-' },
    {
      title: '大小',
      dataIndex: 'size',
      width: 100,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatFileSize(v)}</span>,
    },
    { title: '上传人', dataIndex: 'uploader', width: 100, render: (v?: string) => v ?? '-' },
    {
      title: '上传时间',
      dataIndex: 'uploadedAt',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '业务模块',
      key: 'module',
      width: 120,
      render: (_: unknown, record: FileItem) => record.moduleName ?? resolveModuleLabel(record.module),
    },
    {
      title: '关联单据',
      dataIndex: 'businessNo',
      width: 140,
      ellipsis: true,
      render: (v?: string) => v ?? '-',
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 170,
      render: (_: unknown, record: FileItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          {isImageAttachment(record) && (
            <Button
              type="link"
              size="small"
              icon={<EyeOutlined />}
              loading={previewingId === record.id}
              onClick={() => void handlePreview(record)}
            >
              预览
            </Button>
          )}
          <Button
            type="link"
            size="small"
            icon={<DownloadOutlined />}
            loading={downloadingId === record.id}
            onClick={() => void handleDownload(record)}
          >
            下载
          </Button>
          <SfConfirm
            title="确认删除该文件？"
            description="删除后文件不可恢复，业务侧引用将失效。"
            okText="删除"
            confirming={removeMutation.isPending}
            onConfirm={() => removeMutation.mutate(record.id)}
          >
            <Button type="link" size="small" danger>
              删除
            </Button>
          </SfConfirm>
        </span>
      ),
    },
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="文件中心"
        subtitle="全平台附件与产物文件统一登记（excel.md §7）"
        extra={
          <Button type="primary" icon={<UploadOutlined />} onClick={openUploadModal}>
            上传文件
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'module', label: '业务模块', control: 'select', options: FILE_MODULE_OPTIONS },
            { name: 'business_no', label: '关联单据号', control: 'input', placeholder: '关联单据号' },
          ]}
          onSearch={(values) => {
            setParams(values as FileQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<FileItem>
          storageKey="data-files"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="暂无文件，点击右上角「上传文件」上传"
          scrollX={1160}
        />
      </Card>

      <Modal
        title="上传文件"
        open={uploadOpen}
        width={560}
        maskClosable={false}
        confirmLoading={uploadMutation.isPending}
        okText="上传"
        okButtonProps={{ disabled: pendingFiles.length === 0 }}
        onOk={handleSubmitUpload}
        onCancel={() => {
          if (!uploadMutation.isPending) setUploadOpen(false)
        }}
      >
        <Form<FileUploadFormValues> form={form} layout="vertical">
          <Form.Item
            name="module"
            label="业务模块"
            rules={[{ required: true, message: '请选择业务模块' }]}
            extra="文件将登记到所选业务模块，供按模块筛选与权限过滤（excel.md §7）"
          >
            <Select
              options={FILE_MODULE_OPTIONS}
              placeholder="请选择业务模块"
              showSearch
              optionFilterProp="label"
            />
          </Form.Item>
          <Form.Item name="businessNo" label="关联单据">
            <Input placeholder="选填：关联单据号" maxLength={64} allowClear />
          </Form.Item>
        </Form>
        <SfAttachment
          items={pendingItems}
          title={`待上传文件（${pendingFiles.length}）`}
          uploading={uploadMutation.isPending}
          onUpload={(files) => setPendingFiles((prev) => [...prev, ...files])}
          onDelete={(item) =>
            setPendingFiles((prev) => prev.filter((file, index) => pendingFileKey(file, index) !== item.id))
          }
          uploadLabel="添加文件"
          uploadAccept={FILE_UPLOAD_ACCEPT}
          uploadMultiple
          emptyText="点击「添加文件」选择要上传的文件"
        />
        {uploadMutation.isError && (
          <div style={{ marginTop: 12 }}>
            <SfError
              error={uploadMutation.error}
              description="部分文件可能已上传成功，请刷新列表核对后重试剩余文件。"
            />
          </div>
        )}
      </Modal>

      <Image
        style={{ display: 'none' }}
        src={preview.src || undefined}
        preview={{
          open: preview.open,
          src: preview.src || undefined,
          onOpenChange: closePreview,
        }}
      />
    </div>
  )
}
