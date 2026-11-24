# batch48-go-safety-c-04

模块路径 `gsb/batch48-go-safety-c-04`，包名 `task`，实现文件 `patch_diff_aligner.go`，主类型 `PatchDiffAligner`。

## 题目

上游发安全补丁，本地维护着一条长期定制分支，每次合并补丁都要人工核对半天：行号在定制分支上整体偏移几十行，按 hunk 头里的行号去找全是错位；同一个补丁在两条分支上算出来的指纹不同，因为本地把变量改了名；只加了日志和注释的 hunk 被当成修复，真正加边界检查的 hunk 反而被判成无关改动；某个 hunk 横跨两个函数，归属只能瞎猜；纯删除型 hunk 到底算不算等价修复，每次评审口径都不一样；两个同名静态函数被改名提交挪了位置，hunk 到底属于谁没有结论。

请修 `gsb/batch48-go-safety-c-04` 模块里的 `patch_diff_aligner.go`，类型名 `PatchDiffAligner`，包名 `task`，只用标准库。

接口保持：`NewPatchDiffAligner(Options) *PatchDiffAligner`、`Parse([]byte) ([]Hunk, error)`、`Align(base, target []Line) ([]Mapping, error)`、`Fingerprint(Hunk) (Semantic, error)`、`Classify(Hunk) (Kind, error)`、`Functions([]Line) []Function`、`Normalize(string) string`、`Digest([]Hunk) (Semantic, error)`、`Verify(Mapping, []Line) error`，以及 `Semantic` 上的 `Key` 和 `Hunk` 上的 `Size`。`Parse` 的结果留存在对齐器上，`Align` 基于最近一次解析出的 hunk 工作。

补丁文本按 unified diff 的小节解析：`@@ -旧起,旧数 +新起,新数 @@` 开头，正文行以空格、减号、加号开头；旧数等于上下文行数加减号行数，新数等于上下文行数加加号行数。`Line` 带原始行号与文本，上下文行与删除行按旧行号计，新增行按新行号计。

要落到的十二条：

一、小节头格式不对返回 `ErrBadHeader` 并带行号；正文行数与头部计数不一致返回 `ErrCountMismatch`。

二、上下文行数少于 `MinContext` 返回 `ErrEmptyContext`；整份文本没有小节返回 `ErrBadHeader`。

三、`Parse` 的结果按出现顺序编号，`Index` 从 0 开始，上下文、删除、新增三类行分别保留原始行号。

四、`Align` 按上下文文本在 base 里找唯一落点，允许相对 hunk 头的行号漂移，漂移超过 `MaxShift` 返回 `ErrNoMatch`。

五、同一段上下文在 base 或 target 里出现多次返回 `ErrAmbiguousHunk`，不允许用 hunk 头的行号强行挑一个；零次返回 `ErrNoMatch`。

六、每个 hunk 必须落在唯一的函数体内：函数从 `func` 开头的行起、到下一个函数前一行止；找不到函数返回 `ErrUnknownFunction`，跨两个函数返回 `ErrSpanningFunction`。

七、`Mapping` 带 hunk 序号、函数名、base 与 target 的起始行号与位移，`Shift` 等于 target 起始行号减 base 起始行号，`BaseEnd` 取上下文末行行号；重复对齐输出顺序恒定，按 hunk 序号升序。

八、函数归属遇到同名函数被改名提交挪位、上下文在两处都能对上时，必须报归属歧义而不是任选一个。

九、`Fingerprint` 只取新增行里含比较或条件关键字的行，把标识符统一成占位符后按字典序排列；符号改名不影响结果。

十、`Fingerprint` 忽略新增的日志、注释、赋值等非条件行；同一补丁重复计算、换版本重算都得到相同的 `Checks` 与 `Digest`，单个 hunk 的 `Digest` 与 `Fingerprint` 一致，不依赖 map 遍历。

十一、`Classify` 对新增了条件或边界检查的 hunk 返回 `KindGuard`；只新增注释或空行返回 `KindRefactor`；新增了普通代码返回 `KindUnrelated`；既无新增也无删除返回 `ErrNoChange`。

十二、纯删除型 hunk：删除行里含条件或边界检查时归为 `KindGuardRemoval`，否则按无实质变更处理；`Verify` 发现映射越界或函数区间不匹配返回 `ErrHunkOutOfRange`。

硬约束：解析与对齐全程只用值语义，不做原地修改；同一输入重复执行 `Parse`、`Align`、`Fingerprint` 的字节结果一致；对齐器被并发调用时不得出现半更新的 hunk 列表。验收：`go test ./...` 十五条必须全过，不改测试、不跳过、不按固定输入写死；跨版本重复计算自行验证；只做本地实现。

## 运行

```
go test ./...
```
