package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"aitown/internal/config"
	"aitown/internal/game"
	"aitown/internal/llm"
	"aitown/internal/sim"
	"aitown/internal/workflow"
	"aitown/internal/worldgen"
)

// ---------- 输入清单 ----------

// sourceFiles 参与注释提取的源文件（新增协议/模型文件后在此登记）。
var sourceFiles = []string{
	"internal/game/events.go",
	"internal/game/citizen.go",
	"internal/game/jobs.go",
	"internal/game/engine.go",
	"internal/game/agent.go",
	"internal/sim/sim.go",
	"internal/sim/world.go",
	"internal/workflow/template.go",
	"internal/config/config.go",
	"internal/llm/gateway.go",
	"internal/worldgen/spec.go",
}

var protocolRoots = []reflect.Type{
	reflect.TypeOf(game.SnapshotMsg{}),
	reflect.TypeOf(game.WorldMsg{}),
	reflect.TypeOf(game.EventMsg{}),
	reflect.TypeOf(game.ChatMsg{}),
	reflect.TypeOf(game.BubbleMsg{}),
	reflect.TypeOf(game.EdictMsg{}),
}

var tsRoots = []reflect.Type{
	reflect.TypeOf(game.SnapshotMsg{}),
	reflect.TypeOf(game.WorldMsg{}),
	reflect.TypeOf(game.EventMsg{}),
	reflect.TypeOf(game.ChatMsg{}),
	reflect.TypeOf(game.BubbleMsg{}),
	reflect.TypeOf(game.EdictMsg{}),
	reflect.TypeOf(game.StateData{}),
	reflect.TypeOf(config.Config{}),
}

var modelRoots = []struct {
	group string
	typ   reflect.Type
}{
	{"世界物理（sim）", reflect.TypeOf(sim.Resource{})},
	{"世界物理（sim）", reflect.TypeOf(sim.Building{})},
	{"世界物理（sim）", reflect.TypeOf(sim.Actor{})},
	{"世界物理（sim）", reflect.TypeOf(sim.WorkState{})},
	{"行为模板（workflow）", reflect.TypeOf(workflow.Template{})},
	{"行为模板（workflow）", reflect.TypeOf(workflow.CheckSpec{})},
	{"村民与工作单（game）", reflect.TypeOf(game.Citizen{})},
	{"村民与工作单（game）", reflect.TypeOf(game.Memory{})},
	{"村民与工作单（game）", reflect.TypeOf(game.Job{})},
	{"村民与工作单（game）", reflect.TypeOf(game.WorkEntry{})},
	{"世界初始化（worldgen）", reflect.TypeOf(worldgen.Spec{})},
	{"世界初始化（worldgen）", reflect.TypeOf(worldgen.Villager{})},
	{"配置（config）", reflect.TypeOf(config.Config{})},
	{"配置（config）", reflect.TypeOf(config.ProviderConfig{})},
	{"配置（config）", reflect.TypeOf(config.LimitsConfig{})},
	{"LLM 网关（llm）", reflect.TypeOf(llm.Stats{})},
	{"LLM 网关（llm）", reflect.TypeOf(llm.Message{})},
	{"LLM 网关（llm）", reflect.TypeOf(llm.Request{})},
	{"LLM 网关（llm）", reflect.TypeOf(llm.Response{})},
}

// enumRoots 枚举类型（const 块）：按 常量块首个常量名 定位，值按块内顺序编号。
var enumRoots = []struct {
	group string
	typ   reflect.Type
	file  string
	first string
}{
	{"引擎编排（game）", reflect.TypeOf(game.EvKind(0)), "internal/game/events.go", "EvArrived"},
}

// 编排层内部结构：仅 DATA-MODEL 通道收录（含未导出字段，读者=维护者）。
var internalRoots = []struct {
	group string
	typ   reflect.Type
}{
	{"引擎编排（game）", reflect.TypeOf(game.Engine{})},
	{"引擎编排（game）", reflect.TypeOf(game.Conversation{})},
}

var schemaRoots = []struct {
	name string
	v    any
}{
	{"SnapshotMsg", game.SnapshotMsg{}},
	{"WorldMsg", game.WorldMsg{}},
	{"EventMsg", game.EventMsg{}},
	{"ChatMsg", game.ChatMsg{}},
	{"BubbleMsg", game.BubbleMsg{}},
	{"EdictMsg", game.EdictMsg{}},
	{"StateData", game.StateData{}},
	{"Config", config.Config{}},
}

// ---------- 注释提取 ----------

type structInfo struct {
	doc    string
	fields map[string]string
}

// commentIndex key = "<pkg目录>.<类型名>"，如 "game.SnapshotMsg"。
type commentIndex map[string]*structInfo

// constInfo 枚举常量项。
type constInfo struct {
	Name    string
	Comment string
}

// constIndex 枚举常量块索引，key = "<pkg目录>.<块首常量名>"。
type constIndex map[string][]constInfo

func buildCommentIndex(files []string) (commentIndex, constIndex) {
	idx := commentIndex{}
	cidx := constIndex{}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "docgen: 解析 %s 失败: %v\n", path, err)
			continue
		}
		pkg := pkgNameOf(path)
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gd.Tok {
			case token.TYPE:
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					si := &structInfo{doc: cleanDoc(gd.Doc.Text()), fields: map[string]string{}}
					for _, field := range st.Fields.List {
						comment := cleanDoc(field.Doc.Text())
						if comment == "" {
							comment = cleanDoc(field.Comment.Text())
						}
						for _, n := range field.Names {
							si.fields[n.Name] = comment
						}
					}
					idx[pkg+"."+ts.Name.Name] = si
				}
			case token.CONST:
				var blk []constInfo
				first := ""
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					comment := cleanDoc(vs.Doc.Text())
					if comment == "" {
						comment = cleanDoc(vs.Comment.Text())
					}
					for _, n := range vs.Names {
						if first == "" {
							first = n.Name
						}
						blk = append(blk, constInfo{Name: n.Name, Comment: comment})
					}
				}
				if first != "" {
					cidx[pkg+"."+first] = append(cidx[pkg+"."+first], blk...)
				}
			}
		}
	}
	return idx, cidx
}

// pkgNameOf 从源文件路径或 PkgPath 中提取包目录名：
// "internal/game/events.go" → "game"；"aitown/internal/sim" → "sim"
func pkgNameOf(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "aitown/internal/")
	p = strings.TrimPrefix(p, "internal/")
	if i := strings.Index(p, "/"); i >= 0 {
		return p[:i]
	}
	return p
}

func cleanDoc(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// stripNamePrefix 去掉说明开头的字段名前缀（Go 行内注释惯例："ID 居民 ID" → "居民 ID"）。
func stripNamePrefix(comment, name string) string {
	return strings.TrimSpace(strings.TrimPrefix(comment, name+" "))
}

// ---------- 生成器 ----------

type docgen struct {
	idx    commentIndex
	marked map[string]bool // markdown 段落去重（pkg.Type）
	tsDone map[string]bool // TS interface 去重（pkgPath.Type）
	withTS bool            // 是否同时生成 TS interface（协议通道开启）
	// tsRenames Go 类型名 → TS 类型名 的重命名映射（key = "pkgPath.TypeName"）。
	tsRenames map[string]string
	// tsTLiterals "t" 字段的字面量收窄（key = "pkgPath.TypeName"，如 "snapshot"），
	// 让 SSE 联合类型在 TS 侧可判别。
	tsTLiterals map[string]string
	// includeUnexported 是否收录未导出字段：仅 DATA-MODEL（内部参考）通道开启；
	// PROTOCOL/api.gen.ts（对外契约）永远只出导出字段。
	includeUnexported bool
	md                *strings.Builder
	ts                *strings.Builder
}

// tsName 类型对应的 TS 名（应用重命名映射）。
func (g *docgen) tsName(t reflect.Type) string {
	if r, ok := g.tsRenames[t.PkgPath()+"."+t.Name()]; ok {
		return r
	}
	return t.Name()
}

func (g *docgen) recurse(t reflect.Type) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice ||
		t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return
	}
	if !strings.HasPrefix(t.PkgPath(), "aitown/internal/") {
		return
	}
	g.emitStructMD(t)
	if g.withTS {
		g.emitStructTS(t)
	}
}

func (g *docgen) emitStructMD(t reflect.Type) {
	key := pkgNameOf(t.PkgPath()) + "." + t.Name()
	if g.marked[key] {
		return
	}
	g.marked[key] = true
	si := g.idx[key]
	doc := ""
	if si != nil {
		doc = si.doc
	}
	var rows strings.Builder
	var children []reflect.Type
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() && !g.includeUnexported {
			continue
		}
		name, _, skip := parseJSONTag(f)
		if skip {
			continue
		}
		comment := ""
		if si != nil {
			comment = si.fields[f.Name]
		}
		comment = stripNamePrefix(comment, f.Name) // 行内注释惯以字段名开头，表格里已有"字段"列，去重
		fmt.Fprintf(&rows, "| %s | `%s` | `%s` | %s |\n", f.Name, name, goTypeString(f.Type), comment)
		if ct := structElem(f.Type); ct != nil {
			children = append(children, ct)
		}
	}
	fmt.Fprintf(g.md, "### %s\n\n%s\n\n| 字段 | JSON | Go 类型 | 说明 |\n|---|---|---|---|\n%s\n", t.Name(), doc, rows.String())
	// 表格完整写完后才递归子类型，避免子段落插进父表格中间
	for _, ct := range children {
		g.recurse(ct)
	}
}

// structElem 若字段类型（解包指针/切片后）是本模块内的命名结构体，返回它。
func structElem(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice ||
		t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct && t.Name() != "" && strings.HasPrefix(t.PkgPath(), "aitown/internal/") {
		return t
	}
	return nil
}

func (g *docgen) recurseTS(t reflect.Type) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice ||
		t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return
	}
	if !strings.HasPrefix(t.PkgPath(), "aitown/internal/") {
		return
	}
	g.emitStructTS(t)
}

func (g *docgen) emitStructTS(t reflect.Type) {
	key := t.PkgPath() + "." + t.Name()
	if g.tsDone[key] {
		return
	}
	g.tsDone[key] = true
	si := g.idx[pkgNameOf(t.PkgPath())+"."+t.Name()]
	doc := ""
	if si != nil {
		doc = si.doc
	}
	literal := g.tsTLiterals[key]
	fmt.Fprintf(g.ts, "/** %s */\nexport interface %s {\n", doc, g.tsName(t))
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, skip := parseJSONTag(f)
		if skip {
			continue
		}
		if name == "t" && literal != "" {
			fmt.Fprintf(g.ts, "  /** 消息类型标识。 */\n  t: '%s';\n", literal)
			continue
		}
		opt := ""
		if strings.Contains(opts, "omitempty") {
			opt = "?"
		}
		comment := ""
		if si != nil {
			comment = si.fields[f.Name]
		}
		comment = stripNamePrefix(comment, f.Name)
		fmt.Fprintf(g.ts, "  /** %s */\n  %s%s: %s;\n", comment, name, opt, g.tsType(f.Type))
		g.recurseTS(f.Type)
	}
	g.ts.WriteString("}\n\n")
}

// ---------- 类型映射 ----------

func parseJSONTag(f reflect.StructField) (name, opts string, skip bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, "", false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	opts = strings.Join(parts[1:], ",")
	if name == "-" {
		return "", "", true
	}
	if name == "" {
		name = f.Name
	}
	return name, opts, false
}

func goTypeString(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Ptr:
		return "*" + goTypeString(t.Elem())
	case reflect.Slice, reflect.Array:
		return "[]" + goTypeString(t.Elem())
	case reflect.Map:
		return "map[" + goTypeString(t.Key()) + "]" + goTypeString(t.Elem())
	}
	if t.Name() != "" && strings.HasPrefix(t.PkgPath(), "aitown/internal/") {
		return fmt.Sprintf("[%s](#%s)", t.Name(), strings.ToLower(t.Name()))
	}
	return t.String()
}

var timeType = reflect.TypeOf(time.Time{})

func (g *docgen) tsType(t reflect.Type) string {
	if t == timeType {
		return "string"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Ptr:
		return g.tsType(t.Elem())
	case reflect.Slice, reflect.Array:
		return g.tsType(t.Elem()) + "[]"
	case reflect.Map:
		return "Record<string, " + g.tsType(t.Elem()) + ">"
	}
	if t.Name() != "" {
		return g.tsName(t)
	}
	return "unknown"
}

// writeEnumMD 渲染枚举类型：常量块按源码顺序编号成表。
func writeEnumMD(md *strings.Builder, g *docgen, cidx constIndex, typ reflect.Type, file, first string) {
	pkg := pkgNameOf(typ.PkgPath())
	key := pkg + "." + typ.Name()
	doc := ""
	if si := g.idx[key]; si != nil {
		doc = si.doc
	}
	fmt.Fprintf(md, "### %s\n\n%s\n\n| 常量 | 值 | 说明 |\n|---|---|---|\n", typ.Name(), doc)
	for i, c := range cidx[pkg+"."+first] {
		fmt.Fprintf(md, "| %s | %d | %s |\n", c.Name, i, c.Comment)
	}
	md.WriteString("\n")
}

// ---------- 静态文本 ----------

const protocolHeader = `# 协议参考

> **本文件由 ` + "`cmd/docgen`" + ` 自动生成**——修改协议结构体（internal/game/events.go 等）后运行 ` + "`go run ./cmd/docgen`" + ` 重新生成，勿手改。

传输层两种：
- **REST**：请求-响应（世界初始化 / 指令 / 变速 / 配置）
- **SSE**：` + "`GET /api/events`" + ` 单向事件流，帧格式 ` + "`data: {json}\\n\\n`" + `，15s 心跳，慢消费者丢帧（前端靠 250ms 快照流自愈）

## SSE 事件流分派表

前端按每帧 JSON 的 ` + "`t`" + ` 字段分派（` + "`web/src/net.ts`" + `）：

| t | Go 类型 | 频率 | 说明 |
|---|---|---|---|
| snapshot | SnapshotMsg | 250ms | 时间/库存/居民位置状态/资源量/建筑进度/工作单/token 统计 |
| world | WorldMsg | 世界创建、圈工地、建筑落成 | 全量世界（前端重建静态渲染层） |
| event | EventMsg | 事件驱动 | 日志行（cat: system/world/edict/plan/job/build/chat/reflect） |
| chat | ChatMsg | 对话每句 | 一句对话（前端同时用于气泡与日志） |
| bubble | BubbleMsg | workflow say 节点 | 居民头顶气泡 |
| edict | EdictMsg | 下令时 | 领主指令广播 |

## 帧字段明细

`

const protocolREST = `## REST 端点

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|---|---|---|---|---|
| GET | /api/health | — | {"ok":true} | 存活探针 |
| GET | /api/state | — | StateData + ` + "`config`" + `（掩码） | 页面初始全量状态 |
| POST | /api/world | {"prompt":"...","skip_ai":bool} | 202 {"ok":true} | 创建世界（异步，进度看 SSE system 事件）；skip_ai=true 直接铺内置默认世界（零 LLM） |
| POST | /api/edict | {"text":"..."} | 202 {"ok":true} | 领主指令（异步规划） |
| POST | /api/pause | {"paused":bool} | {"ok":true} | 暂停/继续 |
| POST | /api/save | — | {"ok":true} / 400 {"error"} | 手动存档（同步落盘；游戏每日与退出另有自动存档） |
| POST | /api/continue | — | {"ok":true} / 400 {"error"} | 继续上次存档（启动选择页用；载入后世界帧经 SSE 广播） |
| POST | /api/official | {"id":"居民ID","appoint":bool} | {"ok":true} | 任命/罢免官员 |
| POST | /api/exile | {"id":"居民ID"} | {"ok":true} | 放逐村民 |
| GET | /api/config | — | Config（掩码） | 读配置 |
| POST | /api/config | Config | Config（掩码） | 保存并热生效；掩码密钥自动保留旧值 |
| POST | /api/config/test | {"provider":"名"} | {"ok","latency_ms","error"} | 探测提供商连通性 |
| GET | /api/diag | — | {"stats","debug","tail"} | 诊断快照（LLM 统计 + 日志尾） |
| GET | /api/events | — | SSE 事件流 | 见上表 |
| GET | / | — | SPA | 前端静态资源（内嵌） |

## REST 载荷明细

`

const protocolEnums = `## 附：枚举与约定

**瓦片**（world.tiles 数组值，行优先 idx = y*w+x）：0 草地 · 1 泥土 · 2 道路 · 3 水（不可通行） · 4 沙滩 · 5 农田

**资源 kind**：tree（→木材）· berry（→食物）· rock（→石料）

**建筑 kind**：keep（领主堡/交付点）· house（民居）· granary（粮仓）· farm（农田）

**居民状态**（snapshot.agents[].state）：idle / moving / working / talking / sleeping

**工作单状态**：pending → claimed → done | failed

**时间刻度**：10 tick = 1 真实秒（1x）；150 tick = 1 游戏小时；3600 tick = 1 游戏天 = 6 真实分钟
`

const modelHeader = `# 数据模型参考

> **本文件由 ` + "`cmd/docgen`" + ` 自动生成**——修改领域结构体后运行 ` + "`go run ./cmd/docgen`" + ` 重新生成，勿手改。

领域分层速览：` + "`sim`" + `（确定性世界物理）→ ` + "`workflow`" + `（行为模板）→ ` + "`game`" + `（智能体编排）→ ` + "`llm`" + `（BYOK 网关）；` + "`worldgen`" + ` 只在开局造世界，` + "`config`" + ` 管 BYOK 设置。

`

const tsHeader = `// 本文件由 cmd/docgen 自动生成——协议变更后运行 go run ./cmd/docgen 重新生成，勿手改。
// 字段说明来自后端 Go 结构体注释；与 server 端 JSON 严格对应。
`

// ---------- 输出 ----------

func writeFile(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", path, err)
		os.Exit(1)
	}
	fmt.Println("生成", path)
}

func writeSchemas(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(schemaRoots))
	m := map[string]any{}
	for _, r := range schemaRoots {
		names = append(names, r.name)
		m[r.name] = r.v
	}
	sort.Strings(names)
	for _, name := range names {
		s := jsonschema.Reflect(m[name])
		b, err := json.Marshal(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jsonschema:", name, err)
			os.Exit(1)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			fmt.Fprintln(os.Stderr, "reparse:", name, err)
			os.Exit(1)
		}
		pretty, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "indent:", name, err)
			os.Exit(1)
		}
		writeFile(filepath.Join(dir, name+".schema.json"), string(pretty)+"\n")
	}
}

func main() {
	idx, cidx := buildCommentIndex(sourceFiles)

	// PROTOCOL.md：SSE 帧字段 + REST 载荷（同时产出 TS）——对外契约，仅导出字段
	var protoMD strings.Builder
	protoMD.WriteString(protocolHeader)
	pg := &docgen{idx: idx, marked: map[string]bool{}, tsDone: map[string]bool{}, withTS: true,
		tsRenames: map[string]string{
			"aitown/internal/game.SnapshotMsg": "Snapshot", // 前端沿用历史命名
		},
		tsTLiterals: map[string]string{
			"aitown/internal/game.SnapshotMsg": "snapshot",
			"aitown/internal/game.WorldMsg":    "world",
			"aitown/internal/game.EventMsg":    "event",
			"aitown/internal/game.ChatMsg":     "chat",
			"aitown/internal/game.BubbleMsg":   "bubble",
			"aitown/internal/game.EdictMsg":    "edict",
		},
		md: &protoMD, ts: &strings.Builder{}}
	for _, t := range protocolRoots {
		pg.recurse(t)
	}
	protoMD.WriteString(protocolREST)
	for _, t := range []reflect.Type{reflect.TypeOf(game.StateData{}), reflect.TypeOf(config.Config{}), reflect.TypeOf(config.ProviderConfig{})} {
		pg.recurse(t)
	}
	protoMD.WriteString(protocolEnums)
	writeFile("docs/PROTOCOL.md", protoMD.String())

	// DATA-MODEL.md：内部参考，包含未导出字段 + 枚举表 + 编排层内部结构
	var modelMD strings.Builder
	modelMD.WriteString(modelHeader)
	mg := &docgen{idx: idx, marked: map[string]bool{}, tsDone: map[string]bool{}, includeUnexported: true, md: &modelMD}
	lastGroup := ""
	for _, r := range modelRoots {
		if r.group != lastGroup {
			fmt.Fprintf(&modelMD, "## %s\n\n", r.group)
			lastGroup = r.group
		}
		mg.recurse(r.typ)
	}
	for _, er := range enumRoots {
		if er.group != lastGroup {
			fmt.Fprintf(&modelMD, "## %s\n\n", er.group)
			lastGroup = er.group
		}
		writeEnumMD(&modelMD, mg, cidx, er.typ, er.file, er.first)
	}
	for _, ir := range internalRoots {
		if ir.group != lastGroup {
			fmt.Fprintf(&modelMD, "## %s\n\n", ir.group)
			lastGroup = ir.group
		}
		mg.recurse(ir.typ)
	}
	writeFile("docs/DATA-MODEL.md", modelMD.String())

	// web/src/api.gen.ts
	writeFile("web/src/api.gen.ts", tsHeader+pg.ts.String())

	// docs/schemas/*.json
	writeSchemas("docs/schemas")
}
