# 沙箱可执行白名单的路径与摘要绑定

## 题面

沙箱准入只允许执行白名单里的程序，名单由运维下发，进程启动前统一过一遍准入。安全复盘发现：可执行文件被原地改写后仍按旧记录放行；把同内容文件改名到名单路径也能混进去；脚本通过 env 两段启动时只校验了 env，解释器没校验；热更新名单的瞬间还有请求看到半张表。名单还要支持热更新，运维不希望每次改名单都重启沙箱。要修 gsb/batch48-go-safety-b-04 的 exec_allowlist_guard.go，类型 ExecAllowlistGuard，接口为 NewExecAllowlistGuard(Options) *ExecAllowlistGuard、Allow(Entry) error、Check(ExecRequest) (Decision, error)、Reload([]Entry) error、Snapshot() ([]byte, error)、Restore([]byte) error。

边界要求：
① Allow 只接受绝对路径、存在、带执行位的普通文件，否则报 ErrEntryInvalid，符号链接与目录都不算合法条目。
② Inode 与 Digest 留空时由 guard 从文件补全；显式给出但与实际不符报 ErrEntryMismatch，不能悄悄改成真实值。
③ 每条新条目带递增版本号；Check 的 Decision 带命中条目与版本，版本必须与当前表一致。
④ Check 用服务器固定 PATH 解析裸名，忽略请求 Env 里的 PATH，调用方改环境变量不能换掉解析结果。
⑤ 命中要求路径、inode、摘要同时相等，任一漂移按 metadata-drift 拒绝，不能只看其中一项。
⑥ argv[0] 与实际路径不一致按 argv0-mismatch 拒绝，不能把 argv[0] 当成展示字段。
⑦ 脚本沿 shebang 递归校验解释器，解释器缺失或超过 MaxInterpreterDepth 按 interpreter-missing 拒绝，不能只校验脚本自身；脚本经 env 两段启动时，真正的解释器可能来自 env，不能只校验 env 就放行。
⑧ 未命中给 not-listed；裸名在服务器 PATH 里解析不到同样给 not-listed，不得回退去用调用方路径。
⑨ Entries 按路径排序返回当前表，Lookup 查单条，Revoke 摘除已登记条目，三者都基于同一份现表。
⑩ Reload 先校验整表，任何一条非法报 ErrEntryInvalid 并保留旧表；成功后整表替换、版本递增，旧条目立即失效。
⑪ Record 只接受已允许的请求，否则报 ErrNotAllowed 且不计数；Stats 分别统计 Check、允许、拒绝、Record、Reload，口径不能混。
⑫ 快照保存整表与版本，恢复后 Lookup 与 Check 结果一致；坏快照报 ErrBadSnapshot 且不动现状。

常见错法有四种：只比较路径字符串不比较 inode，改名后的同内容文件就被放行；把 argv[0] 当成提示信息而不拒绝；脚本只看第一行 shebang 不递归；Reload 逐条替换，中途的 Check 看到半张表。

硬约束：并发上，白名单用 atomic.Pointer 换表，Check 只读现表，Reload 不得出现半张表，在途 Check 只能看到旧表或新表。恢复上，快照与换表互斥，恢复同样整表替换；原地改写而路径与 inode 不变时按摘要拒绝，改名到名单路径的同内容文件按 inode 判并给出独立原因。

验收：go test ./... 15 条全过，不得改测试或写死输入；除跑通用例，还要自己补并发与重启场景：Check 与 Reload 交叉、快照后在新 guard 上继续判定、替换解释器后再校验脚本、名单热更新前后判定一致；仅本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-04`
- 实现文件：`exec_allowlist_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ExecAllowlistGuard`
