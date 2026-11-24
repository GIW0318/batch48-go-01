# 命名 rlimit 档案的叠加取交与子进程继承

## 题面

批处理进程启动时要套上多份命名资源档案，限制文件句柄、栈、core 等上限，档案由不同团队维护，启动时按任务批次叠加。上线后出现 open 失败、core 写不全；回滚时又发现有的进程 hard 被抬上去过，堆叠顺序不同结果也不一样，重启后生效值全部丢失。档案数量不多，但叠加顺序由任务配置决定，同一个进程可能先套基础档案再套任务档案。要修 gsb/batch48-go-safety-b-06 的 rlimit_profile_guard.go，类型 RlimitProfileGuard，签名：NewRlimitProfileGuard(Options) *RlimitProfileGuard、Define(name string, limits Limits) error、Apply(pid int, names []string) (Applied, error)、Inherit(parent Applied, child Request) (Applied, error)、Effective(pid int) (Limits, error)、Reset(pid int) error、Snapshot() ([]byte, error)、Restore([]byte) error。

边界要求：
① 空名字或全空白报 ErrBlankName，且不进档案表，定义失败不能留下半条记录，定义成功返回前档案必须已经可查。
② 重名报 ErrDuplicateProfile，原档案保持不变；档案数超过 MaxProfiles 报 ErrProfileLimit，超限定义不入表。
③ 同一资源 soft 高于 hard 报 ErrSoftAboveHard；叠加层数超过 MaxStack 报 ErrStackLimit；空列表报 ErrEmptyStack。
④ Apply 中出现未定义的名字整次拒绝并报 ErrProfileNotFound，进程不留下半个状态，之前的生效值也不动。
⑤ 多档案叠加按资源号取交集，soft 与 hard 各取最小；档案没有写到的资源沿用当前值，不能当成零。
⑥ 对已有状态的进程再次 Apply 只能收紧：hard 请求高于当前值报 ErrHardRaise，整次拒绝并保持原值，不能先改一半再回滚。
⑦ 档案只写 soft 时 hard 沿用当前值，不能把 hard 拉低到新 soft；已降到低 hard 的进程再套更高 hard 档案同样报不可提升。
⑧ Inherit 先用父值与子档案取交得到上下限，再叠加显式提升请求；显式 soft 高于取交后的 hard 报 ErrSoftAboveHard，显式 hard 不得超过父 hard。
⑨ Reset 清掉进程状态，未知 pid 报 ErrUnknownPID，重复 Reset 同样报错；Reset 后再次 Apply 要按全新进程处理。
⑩ 调低 NOFILE 不回收已经打开的 fd，Effective 报告的限额与占用要能分开，不能把占用当成超限。
⑪ 快照保存档案表与每个进程的生效值，恢复后 Profile 与 Effective 一致；坏快照报 ErrBadSnapshot 且不动现网。
⑫ Profile 返回单条档案，Profiles 按定义顺序列出名字，失败的定义不能出现在列表里，顺序也不能被排序打乱。

易漏的地方有三处：档案只写 soft 时 hard 要沿用当前值，不能顺手拉到新 soft；已降到低 hard 的进程再套更高 hard 档案要报不可提升并保持原值；调低 NOFILE 不回收已打开 fd，限额与占用必须分开看。

硬约束：并发上，同一 pid 的 Apply 与 Reset 必须串行，一次 Apply 失败要整体回退，不能让部分资源先生效。容量上，档案数与单次叠加层数是硬顶。恢复上，恢复结果与中断前一致，重复恢复同一份快照结果相同。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死输入；除跑通用例，还要自己补并发与重启场景：同 pid 并发 Apply、Apply 与 Reset 交叉、快照后在新 guard 上继续继承、只写 soft 的档案与显式提权档案的对照；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-06`
- 实现文件：`rlimit_profile_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`RlimitProfileGuard`、`Options`、`Limits`、`Applied`、`Request`
