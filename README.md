# batch48-go-safety-c-08

- 模块路径：`gsb/batch48-go-safety-c-08`
- 包名：`task`
- 实现文件：`sbom_dependency_resolver.go`
- 测试目录：`tests/`

## 题面

供应链治理平台合并了三个事业部的 SBOM，结果同一套镜像解出来的依赖图每次都不一样：有时候锁到 1.2.0，有时候锁到 2.0.0，谁也说不清该用哪个；两个根依赖把同一个组件以不同版本引进来时，工具直接挑了一个，报告里只写“已解析”，审计问起来没法解释。更糟的是有仓库里存在互相依赖的两个包，求解器跑了四十分钟没出结果，最后把整台机器内存吃满。

请修 gsb/batch48-go-safety-c-08 模块里的 sbom_dependency_resolver.go，类型名为 SbomDependencyResolver，包名 task，只用标准库。接口签名保持现状：NewSbomDependencyResolver(ResolverOptions) *SbomDependencyResolver、ParseVersion(string) (Version, error)、Coordinate(Ref) string、AddDocument(SBOM) error、Documents() int、Closure(Ref) ([]Node, error)、Resolve() (Graph, error)、Explain(Conflict) string、Conflicts() []Conflict、Stats() ResolverStats、Reset() error、Snapshot() ([]byte, error)、Restore([]byte) error。SBOM 含 Name、Roots、Components、Edges；Component 含 Group、Name、Version、License、Scope；Edge 含 From、To、Constraint、Scope；Node 含 Coordinate、Version、License、Depth、Scope；Graph 含 Nodes、Edges；Conflict 含 Coordinate、Constraints、Licenses、Reason。

需要修的行为：

一、Coordinate 把 group 与 name 去首尾空白转小写后用斜杠连接，任一段为空视为非法坐标。

二、ParseVersion 只接受三段十进制数字；1.2、1.2.x、v1.2.3、空串一律报错。

三、约束语法为可选前缀加版本，前缀可以是等号、大于等于、小于等于、大于、小于、脱字符、波浪号，缺省按等号处理；脱字符要求同主版本，波浪号要求同主次版本；无法解析的约束让整份文档被拒收。

四、AddDocument：Name 为空、组件版本非法、约束非法返回 ErrInvalidDocument；同名文档返回 ErrDuplicateDocument；超过 MaxDocuments 返回 ErrDocumentLimit；被拒文档不得留下任何组件或边。

五、目录合并：跨文档同坐标同版本的组件合并成一条，许可证取并集；同坐标的不同版本都保留为候选。

六、Closure 从入参坐标做广度优先，根必须出现在目录或任一条边的端点上，否则返回 ErrUnknownCoordinate；节点按深度升序、同深度按坐标字典序输出，重复到达只保留最小深度的一条。

七、闭包遇到回边立即返回 ErrCyclicDependency，错误信息按 cycle: acme/a -> acme/c -> acme/a 的形式列出环上坐标。

八、路径深度超过 MaxDepth 返回 ErrDepthLimit，节点数超过 MaxNodes 返回 ErrNodeLimit。

九、Resolve 对每个可达坐标取目录中满足全部入边约束的最高版本，没有任何候选满足时记一条版本冲突并返回 ErrVersionConflict；Graph 节点按坐标升序，边去重后按起点、终点升序。

十、作用域合并：同坐标同时被运行时与 dev 边引入时节点作用域取 runtime；仅由 dev 路径可达的坐标标记 dev，不进入运行时闭包。

十一、许可证冲突：同一坐标同一版本在目录里出现不同许可证时记一条许可证冲突，Explain 输出形如 acme/lib: divergent licenses MIT, Apache-2.0；版本冲突输出形如 acme/lib: no version satisfies <1.4.0, >=1.4.0，约束按字典序排列。

十二、Snapshot 覆盖文档、目录、冲突与统计，Restore 到空实例后 Resolve 结果逐字段一致，坏字节返回 ErrBadSnapshot；同一输入重复求解结果必须逐字段一致；Stats 返回文档数、组件数、边数与最近一次求解的节点数。

硬约束：Resolve 可以被并发调用且互不干扰，文档写入用写锁串行，任何一次失败的写入不得污染目录；快照必须在写锁内整体生成，不允许出现文档已进但目录未建、或冲突表与图对不上的中间态；MaxDocuments 为零按 8、MaxDepth 为零按 16、MaxNodes 为零按 4096。

验收标准：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
