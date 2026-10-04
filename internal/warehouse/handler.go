package warehouse

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// HTTP handler 层（architecture.md §1：只做参数接收与基础校验、调用 Service、统一响应
// 封装；禁止直连数据库，plan §4.2 判据 2：禁止绕过 internal/response 直接 c.JSON）。
//
// 权限点（plan §5.4.1 冻结清单，常量取自 internal/auth/permissions.go 单一来源）：
// warehouse:warehouse:{list,read,create,update,delete,status}、warehouse:zone/shelf:{list,
// read,create,update,status}（无 delete——zones/shelves 无删除通路）、
// warehouse:bin:{list,read,create,update,delete,status}；库位地图复用 warehouse:bin:list。

// RegisterRoutes 仓库空间域路由（plan §5.2 冻结签名）。约定：
//   - rg 已挂 auth.AuthRequired()；域内对每个路由挂 auth.RequirePermission(...)；
//   - 列表接口强制分页（api.md §2.1）；删除/停用走级联校验（business-flow §1.6）；
//   - Option 仅用于跨域消费接口注入（WithBinOccupancy，plan §4.3/§5.2）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	svc := NewService(newGormRepository(db), WithOccupancyReader(o.occupancy))
	h := &handler{svc: svc}
	_ = rdb // M1 仓库域无直接 Redis 依赖（会话/权限缓存在 auth），参数保留以满足冻结签名

	wh := rg.Group("/warehouses")
	wh.GET("", auth.RequirePermission(auth.PermWarehouseList), h.listWarehouses)
	wh.POST("", auth.RequirePermission(auth.PermWarehouseCreate), h.createWarehouse)
	wh.GET("/:id", auth.RequirePermission(auth.PermWarehouseRead), h.getWarehouse)
	wh.PUT("/:id", auth.RequirePermission(auth.PermWarehouseUpdate), h.updateWarehouse)
	wh.DELETE("/:id", auth.RequirePermission(auth.PermWarehouseDelete), h.deleteWarehouse)
	wh.PUT("/:id/status", auth.RequirePermission(auth.PermWarehouseStatus), h.updateWarehouseStatus)
	wh.GET("/:id/map", auth.RequirePermission(auth.PermBinList), h.warehouseMap) // plan §5.4：map 复用 bin:list

	zo := rg.Group("/zones")
	zo.GET("", auth.RequirePermission(auth.PermZoneList), h.listZones)
	zo.POST("", auth.RequirePermission(auth.PermZoneCreate), h.createZone)
	zo.GET("/:id", auth.RequirePermission(auth.PermZoneRead), h.getZone)
	zo.PUT("/:id", auth.RequirePermission(auth.PermZoneUpdate), h.updateZone)
	zo.PUT("/:id/status", auth.RequirePermission(auth.PermZoneStatus), h.updateZoneStatus)

	sh := rg.Group("/shelves")
	sh.GET("", auth.RequirePermission(auth.PermShelfList), h.listShelves)
	sh.POST("", auth.RequirePermission(auth.PermShelfCreate), h.createShelf)
	sh.GET("/:id", auth.RequirePermission(auth.PermShelfRead), h.getShelf)
	sh.PUT("/:id", auth.RequirePermission(auth.PermShelfUpdate), h.updateShelf)
	sh.PUT("/:id/status", auth.RequirePermission(auth.PermShelfStatus), h.updateShelfStatus)

	bn := rg.Group("/bins")
	bn.GET("", auth.RequirePermission(auth.PermBinList), h.listBins)
	bn.POST("", auth.RequirePermission(auth.PermBinCreate), h.createBin)
	bn.GET("/:id", auth.RequirePermission(auth.PermBinRead), h.getBin)
	bn.PUT("/:id", auth.RequirePermission(auth.PermBinUpdate), h.updateBin)
	bn.DELETE("/:id", auth.RequirePermission(auth.PermBinDelete), h.deleteBin)
	bn.PUT("/:id/status", auth.RequirePermission(auth.PermBinStatus), h.updateBinStatus)
}

type handler struct{ svc *Service }

// scopeOf 数据权限仓库范围快照（auth.WarehouseScope：超管/ALL→全量；
// SPECIFIED_WAREHOUSE→绑定集；其余 M1 未落行级→空集 fail-closed）。禁止前端传参决定。
func scopeOf(c *gin.Context) Scope {
	all, ids := auth.WarehouseScope(c)
	return Scope{All: all, WarehouseIDs: ids}
}

// actorOf 操作者归因（审计字段来源；Scope 一并填入）。
func actorOf(c *gin.Context) Actor {
	uc, _ := auth.CurrentUser(c)
	return Actor{
		UserID:    uc.UserID,
		Username:  uc.Username,
		IsSuper:   uc.IsSuper,
		RequestID: c.GetString(response.RequestIDKey),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Method:    c.Request.Method,
		Path:      c.FullPath(),
		Scope:     scopeOf(c),
	}
}

// pathID 解析路径 ID（api.md §2：ID 一律字符串形态，路由参数解析为正整数）。
func pathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, invalidParam(name, "必须为正整数"))
		return 0, false
	}
	return id, true
}

// queryID 可选整型查询参数（0 = 未提供）。
func queryID(c *gin.Context, name string) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.Err(c, invalidParam(name, "必须为正整数"))
		return 0, false
	}
	return id, true
}

// queryStatus 可选状态查询参数（空 = 全部）。
func queryStatus(c *gin.Context) (string, bool) {
	s := c.Query("status")
	if s == "" || validStatus(s) {
		return s, true
	}
	response.Err(c, invalidParam("status", "ENABLED/DISABLED"))
	return "", false
}

func bindInput[T any](c *gin.Context) (*T, bool) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, response.NewError(response.CodeInvalidParam, response.BindErrorDetails(err)))
		return nil, false
	}
	return &req, true
}

// —— /api/warehouses ——

// listWarehouses GET /api/warehouses。
// @Summary GET /api/warehouses
// @Tags 仓库与库位
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses [get]
func (h *handler) listWarehouses(c *gin.Context) {
	page, pageSize, ok := parsePage(c)
	if !ok {
		return
	}
	status, ok := queryStatus(c)
	if !ok {
		return
	}
	f := WarehouseListFilter{
		Keyword:  c.Query("keyword"),
		Status:   status,
		Type:     c.Query("type"),
		Scope:    scopeOf(c),
		Page:     page,
		PageSize: pageSize,
	}
	items, total, err := h.svc.ListWarehouses(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// createWarehouse POST /api/warehouses。
// @Summary POST /api/warehouses
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body WarehouseCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses [post]
func (h *handler) createWarehouse(c *gin.Context) {
	in, ok := bindInput[WarehouseCreateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.CreateWarehouse(c.Request.Context(), actorOf(c), *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// getWarehouse GET /api/warehouses/:id。
// @Summary GET /api/warehouses/:id
// @Tags 仓库与库位
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses/{id} [get]
func (h *handler) getWarehouse(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.GetWarehouse(c.Request.Context(), scopeOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// updateWarehouse PUT /api/warehouses/:id。
// @Summary PUT /api/warehouses/:id
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body WarehouseUpdateInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses/{id} [put]
func (h *handler) updateWarehouse(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[WarehouseUpdateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdateWarehouse(c.Request.Context(), actorOf(c), id, *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// deleteWarehouse DELETE /api/warehouses/:id。
// @Summary DELETE /api/warehouses/:id
// @Tags 仓库与库位
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses/{id} [delete]
func (h *handler) deleteWarehouse(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteWarehouse(c.Request.Context(), actorOf(c), id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// updateWarehouseStatus PUT /api/warehouses/:id/status。
// @Summary PUT /api/warehouses/:id/status
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body StatusInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses/{id}/status [put]
func (h *handler) updateWarehouseStatus(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[StatusInput](c)
	if !ok {
		return
	}
	if err := h.svc.UpdateWarehouseStatus(c.Request.Context(), actorOf(c), id, in.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": in.Status})
}

// warehouseMap GET /api/warehouses/:id/map（库位地图，plan §5.4）。
// @Summary GET /api/warehouses/:id/map（库位地图，plan §5.4）
// @Tags 仓库与库位
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses/{id}/map [get]
func (h *handler) warehouseMap(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.GetWarehouseMap(c.Request.Context(), scopeOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// —— /api/zones ——

// listZones GET /api/zones。
// @Summary GET /api/zones
// @Tags 仓库与库位
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/zones [get]
func (h *handler) listZones(c *gin.Context) {
	page, pageSize, ok := parsePage(c)
	if !ok {
		return
	}
	warehouseID, ok := queryID(c, "warehouseId")
	if !ok {
		return
	}
	status, ok := queryStatus(c)
	if !ok {
		return
	}
	f := ZoneListFilter{
		WarehouseID: warehouseID,
		Keyword:     c.Query("keyword"),
		ZoneType:    c.Query("zoneType"),
		Status:      status,
		Scope:       scopeOf(c),
		Page:        page,
		PageSize:    pageSize,
	}
	items, total, err := h.svc.ListZones(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// createZone POST /api/zones。
// @Summary POST /api/zones
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body ZoneCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/zones [post]
func (h *handler) createZone(c *gin.Context) {
	in, ok := bindInput[ZoneCreateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.CreateZone(c.Request.Context(), actorOf(c), *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// getZone GET /api/zones/:id。
// @Summary GET /api/zones/:id
// @Tags 仓库与库位
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/zones/{id} [get]
func (h *handler) getZone(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.GetZone(c.Request.Context(), scopeOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// updateZone PUT /api/zones/:id。
// @Summary PUT /api/zones/:id
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body ZoneUpdateInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/zones/{id} [put]
func (h *handler) updateZone(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[ZoneUpdateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdateZone(c.Request.Context(), actorOf(c), id, *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// updateZoneStatus PUT /api/zones/:id/status。
// @Summary PUT /api/zones/:id/status
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body StatusInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/zones/{id}/status [put]
func (h *handler) updateZoneStatus(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[StatusInput](c)
	if !ok {
		return
	}
	if err := h.svc.UpdateZoneStatus(c.Request.Context(), actorOf(c), id, in.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": in.Status})
}

// —— /api/shelves ——

// listShelves GET /api/shelves。
// @Summary GET /api/shelves
// @Tags 仓库与库位
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shelves [get]
func (h *handler) listShelves(c *gin.Context) {
	page, pageSize, ok := parsePage(c)
	if !ok {
		return
	}
	warehouseID, ok := queryID(c, "warehouseId")
	if !ok {
		return
	}
	zoneID, ok := queryID(c, "zoneId")
	if !ok {
		return
	}
	status, ok := queryStatus(c)
	if !ok {
		return
	}
	f := ShelfListFilter{
		WarehouseID: warehouseID,
		ZoneID:      zoneID,
		Keyword:     c.Query("keyword"),
		Status:      status,
		Scope:       scopeOf(c),
		Page:        page,
		PageSize:    pageSize,
	}
	items, total, err := h.svc.ListShelves(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// createShelf POST /api/shelves。
// @Summary POST /api/shelves
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body ShelfCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shelves [post]
func (h *handler) createShelf(c *gin.Context) {
	in, ok := bindInput[ShelfCreateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.CreateShelf(c.Request.Context(), actorOf(c), *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// getShelf GET /api/shelves/:id。
// @Summary GET /api/shelves/:id
// @Tags 仓库与库位
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shelves/{id} [get]
func (h *handler) getShelf(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.GetShelf(c.Request.Context(), scopeOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// updateShelf PUT /api/shelves/:id。
// @Summary PUT /api/shelves/:id
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body ShelfUpdateInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shelves/{id} [put]
func (h *handler) updateShelf(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[ShelfUpdateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdateShelf(c.Request.Context(), actorOf(c), id, *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// updateShelfStatus PUT /api/shelves/:id/status。
// @Summary PUT /api/shelves/:id/status
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body StatusInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/shelves/{id}/status [put]
func (h *handler) updateShelfStatus(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[StatusInput](c)
	if !ok {
		return
	}
	if err := h.svc.UpdateShelfStatus(c.Request.Context(), actorOf(c), id, in.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": in.Status})
}

// —— /api/bins ——

// listBins GET /api/bins。
// @Summary GET /api/bins
// @Tags 仓库与库位
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/bins [get]
func (h *handler) listBins(c *gin.Context) {
	page, pageSize, ok := parsePage(c)
	if !ok {
		return
	}
	warehouseID, ok := queryID(c, "warehouseId")
	if !ok {
		return
	}
	zoneID, ok := queryID(c, "zoneId")
	if !ok {
		return
	}
	shelfID, ok := queryID(c, "shelfId")
	if !ok {
		return
	}
	status, ok := queryStatus(c)
	if !ok {
		return
	}
	f := BinListFilter{
		WarehouseID: warehouseID,
		ZoneID:      zoneID,
		ShelfID:     shelfID,
		Keyword:     c.Query("keyword"),
		BinType:     c.Query("binType"),
		Status:      status,
		Scope:       scopeOf(c),
		Page:        page,
		PageSize:    pageSize,
	}
	items, total, err := h.svc.ListBins(c.Request.Context(), f)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, page, pageSize, total)
}

// createBin POST /api/bins。
// @Summary POST /api/bins
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body BinCreateInput true "请求体"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/bins [post]
func (h *handler) createBin(c *gin.Context) {
	in, ok := bindInput[BinCreateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.CreateBin(c.Request.Context(), actorOf(c), *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// getBin GET /api/bins/:id。
// @Summary GET /api/bins/:id
// @Tags 仓库与库位
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/bins/{id} [get]
func (h *handler) getBin(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.GetBin(c.Request.Context(), scopeOf(c), id)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// updateBin PUT /api/bins/:id。
// @Summary PUT /api/bins/:id
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body BinUpdateInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/bins/{id} [put]
func (h *handler) updateBin(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[BinUpdateInput](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdateBin(c.Request.Context(), actorOf(c), id, *in)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, v)
}

// deleteBin DELETE /api/bins/:id。
// @Summary DELETE /api/bins/:id
// @Tags 仓库与库位
// @Produce json
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/bins/{id} [delete]
func (h *handler) deleteBin(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteBin(c.Request.Context(), actorOf(c), id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// updateBinStatus PUT /api/bins/:id/status。
// @Summary PUT /api/bins/:id/status
// @Tags 仓库与库位
// @Accept json
// @Produce json
// @Param body body StatusInput true "请求体"
// @Param id path int true "路径参数 id"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/bins/{id}/status [put]
func (h *handler) updateBinStatus(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	in, ok := bindInput[StatusInput](c)
	if !ok {
		return
	}
	if err := h.svc.UpdateBinStatus(c.Request.Context(), actorOf(c), id, in.Status); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"status": in.Status})
}

// parsePage 分页参数解析（统一走 response.ParsePage，禁止各页自造）。
func parsePage(c *gin.Context) (page, pageSize int, ok bool) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return 0, 0, false
	}
	return page, pageSize, true
}
