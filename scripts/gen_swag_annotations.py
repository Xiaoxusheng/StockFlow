#!/usr/bin/env python3
"""F8 swag 注解生成器（清偿轮一次性工具，可重跑幂等）。

从 gin 运行时路由 dump（internal/router/routes_dump.json，由临时测试装配引擎
r.Routes() 产出——保证不编造不存在的路由）为每个 handler 函数生成最小集注解：
@Summary/@Tags/@Accept/@Produce/@Param/@Success/@Failure/@Router。

规则：
- 已有 @Summary 的函数跳过（幂等）；
- Summary 取自函数既有 doc 注释（去函数名前缀），无注释时用 "METHOD path"；
- @Tags 按域中文标签映射；多路由函数合并多条 @Router；
- @Param：路径参数（:x → {x}）+ POST/PUT/PATCH 请求体类型（从函数体 ShouldBindJSON/
  bindJSON/bindInput 的绑定变量声明推导；json.RawMessage → map[string]any）；
- @Success/@Failure 统一引用 internal/response.Envelope。
"""
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DUMP = ROOT / "internal/router/routes_dump.json"

TAGS = {
    "auth": "认证与用户", "masterdata": "基础资料", "warehouse": "仓库与库位",
    "inventory": "库存", "purchase": "采购入库", "sales": "销售出库",
    "stockops": "库存作业", "returns": "退货与异常", "datax": "数据中心",
    "printing": "打印中心", "devices": "设备中心", "reports": "报表",
    "sysops": "系统运维", "health": "健康检查", "router": "路由",
}

HANDLER_RE = re.compile(r"^(?:\(\w+ \*?\w+\)\.)?(\w+)(?:-fm)?$")
PATH_RE = re.compile(r"/:([A-Za-z0-9_]+)")


def parse_handler(raw: str):
    # github.com/stockflow/server/internal/auth.handleLogin
    # github.com/stockflow/server/internal/sysops.(*handler).markNotificationRead-fm
    seg = raw.split("/internal/", 1)[1]
    m = re.match(r"^([a-z0-9_]+)\.(?:\(\*?(\w+)\)\.)?(\w+?)(?:-fm)?$", seg)
    if not m:
        return None
    pkg = m.group(1)
    recv = m.group(2)
    fn = m.group(3)
    if fn.endswith(".func") and re.search(r"\.func\d+$", seg):
        return None  # 闭包形态（RegisterRoutes.funcN），另行处理
    return pkg, recv, fn


def find_func(pkg: str, recv, fn: str):
    """在 internal/<pkg> 下定位函数声明，返回 (file, line_idx(0基), 行文本)。"""
    d = ROOT / "internal" / pkg
    if not d.is_dir():
        return None
    if recv:
        pat = re.compile(rf"^func \(\w+ \*?{re.escape(recv)}\) {re.escape(fn)}\(")
    else:
        pat = re.compile(rf"^func {re.escape(fn)}\(")
    hits = []
    for f in sorted(d.glob("*.go")):
        if f.name.endswith("_test.go"):
            continue
        for i, line in enumerate(f.read_text(encoding="utf-8").splitlines()):
            if pat.match(line):
                hits.append((f, i, line))
    return hits[0] if len(hits) == 1 else None if not hits else ("AMBIG", hits, None)


def comment_summary(lines, func_idx):
    """取紧邻 func 的 doc 注释块首行描述（去函数名前缀）。"""
    out = []
    i = func_idx - 1
    while i >= 0 and lines[i].lstrip().startswith("//"):
        out.append(lines[i].strip()[2:].strip())
        i -= 1
    if not out:
        return None
    first = out[-1]  # 注释块首行
    first = re.sub(rf"^\S+\s+", "", first, count=1)  # 去函数名
    # 剥离 "（GET /api/...）" 方法段（全角括号用 unicode 转义，避免字符类歧义）
    first = re.sub(r"\s*[\uff08]?\s*(GET|POST|PUT|DELETE|PATCH)\s+/api/[^\uff09\u3002]*[\uff09]?", "", first, count=1)
    first = first.strip("\uff08\uff09\u3002;； ")
    return first or None


def bind_type(lines, func_idx):
    """在函数体内找 ShouldBindJSON/bindJSON/bindInput 绑定的类型。"""
    end = len(lines)
    depth = 0
    started = False
    body = []
    for i in range(func_idx, min(func_idx + 200, len(lines))):
        body.append(lines[i])
        depth += lines[i].count("{") - lines[i].count("}")
        if "{" in lines[i]:
            started = True
        if started and depth <= 0:
            break
    seg = "\n".join(body)
    m = re.search(r"bind(?:JSON|Input)(?:\[([\w\[\]\*\.]+)\])?\(", seg)
    if m and m.group(1):
        t = m.group(1)
        return None if "*" in t else t
    varm = re.search(r"ShouldBindJSON\(&(\w+)\)", seg) or re.search(r"bind(?:JSON|Input)\([^,]*&(\w+)\)", seg)
    if varm:
        var = varm.group(1)
        tm = re.search(rf"var {re.escape(var)} ([\w\[\]\*\.\d]+)", seg)
        if tm:
            return tm.group(1)
    return None


def resolve_closure(pkg: str, method: str, path: str):
    """RegisterRoutes.funcN 闭包：按 (method, 相对路径) 反查注册行内委托的具名函数。

    注册形态：rg.GET("/products", auth.RequirePermission(...), func(c *gin.Context) { handleProductList(c, svc) })
    返回 (接收器 or None, 目标函数名)；解析失败返回 None。
    """
    rel = re.sub(r"^\./", "", path)
    rel = re.sub(r"^\(/api\)", "", rel)
    if rel.startswith("/api/"):
        rel = rel[len("/api"):]
    rel = PATH_RE.sub(lambda m: "/:" + m.group(1), rel)
    d = ROOT / "internal" / pkg
    line_re = re.compile(rf"""\.{method.upper()}\(\"{re.escape(rel)}\"""")
    call_re = re.compile(r"func\(c \*gin\.Context\) \{ ?(\w+)\(")
    for f in sorted(d.glob("*.go")):
        if f.name.endswith("_test.go"):
            continue
        for line in f.read_text(encoding="utf-8").splitlines():
            if line_re.search(line):
                cm = call_re.search(line)
                if cm:
                    return None, cm.group(1)
                return None, None
    return None, None


def main():
    rows = json.loads(DUMP.read_text(encoding="utf-8"))
    by_func = defaultdict(list)
    for r in rows:
        h = parse_handler(r["handler"])
        if h is None:
            # 闭包形态：RegisterRoutes.funcN / Liveness.func1 → 反查委托的具名函数
            m = re.match(r"^github\.com/stockflow/server/internal/([a-z0-9_]+)\.\w+\.func\d+$", r["handler"])
            if m:
                recv, fn = resolve_closure(m.group(1), r["method"], r["path"])
                if fn:
                    by_func[(m.group(1), recv, fn)].append(r)
                    continue
            print("!! 无法解析 handler:", r["handler"], file=sys.stderr)
            continue
        pkg, recv, fn = h
        by_func[(pkg, recv, fn)].append(r)

    # 文件 → [(插入行号, 注解块)]，按文件聚合后从底向上插入。
    edits = defaultdict(list)
    skipped, nobind = [], []
    for (pkg, recv, fn), rs in sorted(by_func.items()):
        loc = find_func(pkg, recv, fn)
        if loc is None or loc[0] == "AMBIG":
            print(f"!! 定位失败/歧义: {pkg} ({recv}).{fn} -> {loc}", file=sys.stderr)
            continue
        f, idx, line = loc
        lines = f.read_text(encoding="utf-8").splitlines()
        # 幂等：上方注释块已含 @Summary 则跳过
        i = idx - 1
        block = []
        while i >= 0 and lines[i].lstrip().startswith("//"):
            block.insert(0, lines[i])
            i -= 1
        if any("@Summary" in b for b in block):
            skipped.append(f"{pkg}/{fn}")
            continue

        method = rs[0]["method"].lower()
        routers = []
        for r in rs:
            sw_path = PATH_RE.sub(r"/{\1}", r["path"])
            routers.append(f"// @Router {sw_path} [{r['method'].lower()}]")
        tag = TAGS.get(pkg, pkg)
        summary = comment_summary(lines, idx)
        if not summary:
            summary = f"{rs[0]['method']} {rs[0]['path']}"

        ann = [f"// @Summary {summary}", f"// @Tags {tag}", "// @Produce json"]
        t = bind_type(lines, idx)
        has_body = rs[0]["method"] in ("POST", "PUT", "PATCH")
        if has_body:
            ann.insert(2, "// @Accept json")
            if t:
                ann.append(f'// @Param body body {t} true "请求体"')
            else:
                nobind.append(f"{pkg}/{fn}")
        # 路径参数
        for p in PATH_RE.findall(rs[0]["path"]):
            ann.append(f'// @Param {p} path int true "路径参数 {p}"')
        ann.append("// @Success 200 {object} response.Envelope \"统一响应信封\"")
        ann.append("// @Failure 400 {object} response.Envelope \"请求参数错误\"")
        ann.extend(routers)
        edits[f].append((idx, ann))

    total = 0
    for f, items in edits.items():
        lines = f.read_text(encoding="utf-8").splitlines()
        for idx, ann in sorted(items, key=lambda x: -x[0]):
            # 注解并入 doc 注释块：紧跟既有注释块之后（或 func 前新起注释块）
            insert_at = idx
            lines.insert(insert_at, "\n".join(ann))  # 先占位拼接
            lines[insert_at:insert_at + 1] = ann
            total += len(ann)
        f.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"OK: 注解行 {total} 条，跳过（已有注解）{len(skipped)}，缺请求体类型 {len(nobind)}")
    if nobind:
        print("缺请求体类型（仅 @Accept，无 @Param body）:", *nobind, sep="\n  ")


if __name__ == "__main__":
    main()
