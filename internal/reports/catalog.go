package reports

// 报表目录（backend-m3-plan §9.1：GET /api/reports 为代码内冻结注册表，无目录表；
// requirements §2.4 报表中心）。M3 交付 plan §9.1 冻结最小集 + 前端先行目录契约字段
// （web/src/api/reports.ts ReportCatalogItem：key/name/category/description）。

// reportCatalogItem 报表目录项。
type reportCatalogItem struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}

// reportCatalog 冻结报表目录（plan §9.1 报表端点表一一对应；分类沿用前端契约四类：
// 库存/单据/分析/效率——M3 只交付前三类中有数据面的报表，仓库/人员效率无流水
// 语义输入，不进入目录（禁止假报表，requirements §10））。
var reportCatalog = []reportCatalogItem{
	{
		Key:         "inventory-summary",
		Name:        "库存汇总",
		Category:    "库存",
		Description: "按仓库/SKU 分组的库存状态与金额汇总（可用/锁定/冻结/待检/残次 + 库存金额）",
	},
	{
		Key:         "inbound-stats",
		Name:        "入库统计",
		Category:    "单据",
		Description: "按日的入库单数/入库数量/入库金额趋势（口径：INBOUND 流水落账；金额按成本价估值）",
	},
	{
		Key:         "outbound-stats",
		Name:        "出库统计",
		Category:    "单据",
		Description: "按日的出库单数/出库数量/出库金额趋势（口径：OUTBOUND 流水落账；金额按销售价估值）",
	},
	{
		Key:         "inventory-turnover",
		Name:        "库存周转",
		Category:    "分析",
		Description: "按仓库/SKU 的出库量与平均库存（期初期末均值口径）计算的周转率与周转天数",
	},
	{
		Key:         "stagnant-stock",
		Name:        "库存积压",
		Category:    "分析",
		Description: "30/60/90 天未动库存清单（末次移动时间口径，与长期库存扫描同源）",
	},
	{
		Key:         "replenishment-suggestions",
		Name:        "智能补货建议",
		Category:    "分析",
		Description: "按安全库存与近期出库速率计算的补货建议（只读建议，附计算依据字段，不改库存）",
	},
}

// Catalog 返回冻结报表目录（副本，防调用方篡改注册表）。
func Catalog() []reportCatalogItem {
	out := make([]reportCatalogItem, len(reportCatalog))
	copy(out, reportCatalog)
	return out
}
