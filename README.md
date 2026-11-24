# batch48-go-safety-c-01

模块路径 `gsb/batch48-go-safety-c-01`，包名 `task`，实现文件 `detection_rule_compiler.go`，主类型 `DetectionRuleCompiler`。

## 题目

规则平台每次版本更新都要把上千条文本规则编译成可下发的规则包，最近连续三次发版出事：两条同名规则被写进同一份源，编译照过，下发后只有后一条生效；两份规则互相依赖，编译通过，线上扫描互相等待到超时；带十六进制跳转的规则把偏移写成 256，越界没人拦，命中位置整体漂移；尾巴上叠了多层量词的正则把扫描线程拖死，回溯预算形同虚设。同一份源在两台机器上编出来的规则顺序还不一样，比对校验和对不上；并发编译同一份源时会同时发出两个包，正在被扫描引用的旧包被提前释放，进程直接崩溃。

请修 `gsb/batch48-go-safety-c-01` 模块里的 `detection_rule_compiler.go`，类型名 `DetectionRuleCompiler`，包名 `task`，只用标准库。

对外接口保持这些署名：`NewDetectionRuleCompiler(Options) *DetectionRuleCompiler`、`Compile(src []byte) (*RulePack, error)`、`Validate(*RulePack) []Diagnostic`、`BindDeps(*RulePack, map[string]uint64) error`、`EstimateCost(*RulePack) (Budget, error)`、`Publish(*RulePack) (*RulePack, error)`、`Acquire() (*RulePack, error)`、`Release(*RulePack) error`、`Active() *RulePack`、`Reload(src []byte) (*RulePack, error)`、`Stats() CompilerStats`，以及 `RulePack` 上的 `OrderedIDs`、`Rule`、`Digest`、`Bytes`。

源文件每行一条规则，七个字段用竖线分隔：`id | pattern | jump | mods | deps | conflicts | severity`。`jump` 写 `-` 或 `0xLO-0xHI@OFF`；`mods` 写 `-` 或 `i/s/m` 的逗号列表；`deps` 写 `-` 或逗号列表，外部依赖写成 `名字@最低版本`。

要落到行为上的十一条：

一、同一份源出现重复 id 返回 `ErrDuplicateRule`，错误文本带该 id 与行号。

二、内部依赖成环返回 `ErrDependencyCycle`，错误文本带环上至少两个 id。

三、依赖既不来自本包也未标外部版本时返回 `ErrUnknownDependency`，错误文本带缺失名字。

四、未绑定外部依赖就 `Publish` 返回 `ErrPackNotBound`；`BindDeps` 成功后把解析版本写进 `Resolved`，`known` 里缺包返回 `ErrUnknownDependency`。

五、`jump` 偏移超过 255、hi 超过 0xFF 或 lo 大于 hi 返回 `ErrJumpOutOfRange`，错误文本带规则 id。

六、单条规则回溯估算超过 `MaxBacktrack` 时 `Compile` 返回 `ErrCostBudget` 并带规则 id；回溯按 `(q+1)*(a+1)` 计，`q` 是 `*` 与 `+` 的个数，`a` 是 `|` 的个数。

七、原子数按不属于 `.*+?()[]{}|\` 的字符计，每规则至少一个；`Bytes` 是 pattern 长度之和；`EstimateCost` 返回精确数值。

八、`Order` 是拓扑序，被依赖者在前，同层按源文件出现顺序；同一源重复编译的 `Bytes` 与 `Digest` 逐字节一致，不得依赖 map 遍历。

九、包内两条规则互相声明 `conflicts` 返回 `ErrMutualExclusion` 并带两个 id；`Validate` 对重复 id、悬空依赖、互斥冲突分别给出带规则标识的 `Diagnostic`。

十、并发对同一份源编译后发布只产生一个在役包；旧包引用归零前不得释放：`Acquire` 后发新包只增加 `Retired`，`Release` 之后 `Released` 才加一。

十一、`Reload` 解析失败时保留原在役包并返回错误；`jump` 与不区分大小写修饰符同时出现的规则只记一条 `CodeJumpCaseInsensitive` 告警而不拒绝；被依赖包版本号回退时整包拒绝并继续用旧包服务。

硬约束：状态变更一律加锁，发布与借用不能出现半成品；重启后用 `Reload` 重放同一份源必须得到相同 `Order` 与 `Digest`。验收：`go test ./...` 十五条必须全过，不改测试、不跳过、不按固定输入写死；并发与重载自行验证；只做本地实现。

## 运行

```
go test ./...
```
