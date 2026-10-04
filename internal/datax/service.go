package datax

import (
	"context"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/storage"
	"go.uber.org/zap"
)

// Service 数据中心业务层（architecture.md §1：业务逻辑/状态机/事务边界/校验）。
// 依赖以窄接口注入（Repository + 本包 ImportWriter/ExportSource 注册表 + asynqx.Queue +
// storage.Store），单元测试以内存替身替换。
type Service struct {
	repo Repository
	// store 文件中心存储（internal/storage：上传安全校验 + 原子落盘 + 流式打开）。
	store *storage.Store
	// queue 异步任务队列（asynqx 基座；inline 模式 Enqueue 即同步执行，plan §4.1）。
	queue asynqx.Queue
	// writers 导入写入器注册表（九类；router 装配经 Option 注入，缺位 fail-closed）。
	writers map[string]ImportWriter
	// sources 导出行源注册表（16 模块；REPORT 归 MT4，缺位创建时 409 拒绝）。
	sources map[string]ExportSource
	cfg     Config
	log     *zap.Logger
}

// Config 运行参数（来源 config.DataxConfig/StorageConfig，plan §3.2 冻结键）。
type Config struct {
	// ImportMaxRows 单次导入行数上限（超限 DATAX_TOO_MANY_ROWS）。
	ImportMaxRows int
	// BatchSize 导入分批批大小（excel §6.2）。
	BatchSize int
	// ExportBatchSize 导出流式批次（keyset 游标每批行数）。
	ExportBatchSize int
	// FileRetentionDays 任务产物/上传文件保留天数（files.expires_at 推导）。
	FileRetentionDays int
	// UploadMaxBytes 上传大小上限（api.md §5；经路由组局部中间件 + storage.Save 双保险）。
	UploadMaxBytes int64
}

// 预览行数上限（plan §6.2：前 100 行）。
const previewRows = 100

// ExportMaxFileBytes 导出产物文件大小上限（导出体量独立于交互式上传上限 20MB；
// 10 万行 × 12 列量级 < 100MB，超限任务失败落 error_message，防磁盘写爆）。
const ExportMaxFileBytes = 200 << 20

// Actor 操作者上下文（handler 自 gin 提取传入，供审计与流水归因；形态对齐 returns.Actor）。
type Actor struct {
	UserID    int64
	Username  string
	RequestID string
	IP        string
	UserAgent string
	Method    string
	Path      string
	// AllWarehouses/WarehouseIDs 仓库数据权限快照（auth.WarehouseScope 口径：
	// ALL/超管 → (true, nil)；SPECIFIED_WAREHOUSE → (false, ids)；其余 → (false, nil)）。
	// 随确认载荷透传给执行器，域内写入器据此构建数据权限 Scope（plan §13.6）。
	AllWarehouses bool
	WarehouseIDs  []int64
}

// actorFileScope Actor → FileScope（CreateExport/Validate 等 Service 内部路径的任务
// 产物元数据可见性判定；与 handler fileScopeOf 同一仓库范围快照口径）。
func actorFileScope(a Actor) *FileScope {
	return &FileScope{All: a.AllWarehouses, WarehouseIDs: a.WarehouseIDs, UserID: a.UserID}
}

// auditEntry 构造 datax 域审计条目骨架（module=datax；快照由调用方补充；
// plan §13.8：确认导入/创建导出/下载文件/删除文件等敏感操作经 middleware.Audit）。
func (a Actor) auditEntry(objectType string, objectID int64, action string) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module:       "datax",
		ObjectType:   objectType,
		Action:       action,
		ObjectID:     objectID,
		OperatorID:   a.UserID,
		OperatorName: a.Username,
		RequestID:    a.RequestID,
		IP:           a.IP,
		UserAgent:    a.UserAgent,
		Method:       a.Method,
		Path:         a.Path,
	}
}

// NewService 构建业务层（writers/sources 经 Option 注入；queue/store 必需，缺 nil 启动失败）。
func NewService(repo Repository, store *storage.Store, queue asynqx.Queue, cfg Config, log *zap.Logger, opts ...Option) *Service {
	if cfg.ImportMaxRows <= 0 {
		cfg.ImportMaxRows = 5000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 200
	}
	if cfg.ExportBatchSize <= 0 {
		cfg.ExportBatchSize = 1000
	}
	if cfg.FileRetentionDays <= 0 {
		cfg.FileRetentionDays = 30
	}
	if cfg.UploadMaxBytes <= 0 {
		cfg.UploadMaxBytes = storage.DefaultUploadMaxBytes
	}
	if log == nil {
		log = zap.NewNop()
	}
	o := &options{writers: map[string]ImportWriter{}, sources: map[string]ExportSource{}}
	for _, opt := range opts {
		opt(o)
	}
	return &Service{
		repo:    repo,
		store:   store,
		queue:   queue,
		writers: o.writers,
		sources: o.sources,
		cfg:     cfg,
		log:     log,
	}
}

// ---- 装配 Option（plan §12.2：窄接口实现由 router 注入）----

type options struct {
	writers map[string]ImportWriter
	sources map[string]ExportSource
}

// Option RegisterRoutes 的可选注入项。
type Option func(*options)

// WithImportWriter 注册导入写入器（九类实现落各域 datax_import.go，router 装配注入；
// 重复注册同类型视为装配错误，启动期 panic 快速失败）。
func WithImportWriter(importType string, w ImportWriter) Option {
	return func(o *options) {
		if _, dup := o.writers[importType]; dup {
			panic("datax 装配失败: 导入类型 " + importType + " 重复注册 Writer")
		}
		o.writers[importType] = w
	}
}

// WithExportSource 注册导出行源（16 模块实现落各域 datax_export.go；REPORT 归 MT4）。
func WithExportSource(module string, s ExportSource) Option {
	return func(o *options) {
		if _, dup := o.sources[module]; dup {
			panic("datax 装配失败: 导出模块 " + module + " 重复注册 Source")
		}
		o.sources[module] = s
	}
}

// ---- 内部助手 ----

// writerFor 取写入器（未注册返回 ErrImportTypeInvalid——fail-closed 暴露装配缺位）。
func (s *Service) writerFor(importType string) (ImportWriter, error) {
	if !IsValidImportType(importType) {
		return nil, response.NewError(ErrImportTypeInvalid, map[string]any{"import_type": importType})
	}
	w, ok := s.writers[importType]
	if !ok || w == nil {
		return nil, response.NewError(ErrImportTypeInvalid, map[string]any{
			"import_type": importType, "reason": "writer_not_wired",
		})
	}
	return w, nil
}

// sourceFor 取行源（REPORT 行源归 MT4；缺位 409 拒绝创建任务）。
func (s *Service) sourceFor(module string) (ExportSource, error) {
	if !IsValidExportModule(module) {
		return nil, response.NewError(ErrModuleInvalid, map[string]any{"module": module})
	}
	src, ok := s.sources[module]
	if !ok || src == nil {
		return nil, response.NewError(ErrModuleNotAvailable, map[string]any{
			"module": module, "reason": "source_not_wired",
		})
	}
	return src, nil
}

// inTx 事务包装（database.Tx：业务写入与审计同事务——architecture.md §4）。
func (s *Service) inTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return database.Tx(ctx, s.repo.DB(), fn)
}

// audit 事务内写审计（tx 必须非 nil——middleware.Audit 契约）。
func (s *Service) audit(tx *gorm.DB, e middleware.AuditEntry) error {
	return middleware.Audit(tx, e)
}

// auditStandalone 独立审计写入（下载等无业务事务的敏感操作：审计行 INSERT 单独提交；
// 失败仅记日志不阻断下载——读取侧降级，写入侧仍强制同事务，口径与 devices resolve 降级一致）。
func (s *Service) auditStandalone(ctx context.Context, e middleware.AuditEntry) {
	db := s.repo.DB().WithContext(ctx)
	if err := middleware.Audit(db, e); err != nil {
		s.log.Error("datax: 独立审计写入失败", zap.String("action", e.Action),
			zap.Int64("object_id", e.ObjectID), zap.Error(err))
	}
}

// responseNotFound 任务/文件不存在统一错误。
func responseNotFound() error {
	return response.NewError(ErrTaskNotFound, nil)
}

// truncateMessage 错误消息截断（error_message/告警内容防超长刷屏；按字节限长但
// 回退到完整 UTF-8 rune 边界，避免切断多字节字符产生非法序列）。
func truncateMessage(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := []byte(s[:max])
	for len(b) > 0 && !utf8.RuneStart(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	if len(b) > 0 {
		if r, size := utf8.DecodeLastRune(b); r == utf8.RuneError && size == 1 {
			b = b[:len(b)-1] // 切点落在多字节字符起始字节上——去掉该不完整字节
		}
	}
	return string(b) + "…"
}

// expiry 文件保留期推导（datax.file_retention_days，excel §3/§5；NULL=不过期）。
func (s *Service) expiry() *time.Time {
	t := time.Now().AddDate(0, 0, s.cfg.FileRetentionDays)
	return &t
}

// rawToJSON map[string]string → jsonb 载体。
func rawToJSON(m map[string]string) JSONMap {
	out := make(JSONMap, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// jsonToRaw jsonb 载体 → map[string]string（非字符串值丢弃——raw 列仅存原始文本）。
func jsonToRaw(m JSONMap) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if sv, ok := v.(string); ok {
			out[k] = sv
		}
	}
	return out
}

// rawString 读 raw 单元格文本（缺列/非字符串返回空）。
func rawString(m JSONMap, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
