# 实体快照插值播放时钟：抖动缓冲、重复 tick 与瞬移抑制

## 题面

回放服上线后，观战端每隔几秒就出现角色抽搐：位置在两帧之间来回弹，偶尔先退回上一段再瞬移回来；弱网时角色干脆停在原地不动，界面也不提示数据已经过期；运维看丢弃计数始终是 0，抖动的证据全丢了。落点 gsb/batch48-go-game-a-11 的 snapshot_interpolation_clock.go，类型 InterpolationClock，签名保持：NewInterpolationClock(tickRate int, initialDelay time.Duration) *InterpolationClock、Push(s Snapshot) error、Sample(tick uint64) (State, bool)、SetDelay(d time.Duration)、Delay() time.Duration、SetExtrapolationLimit(d time.Duration)、SetTeleportThreshold(v float64)、Queued() int、Dropped() int、Ticks() []uint64、TickSpan() time.Duration、Stale() bool、Snapshot() ([]byte, error)、Restore([]byte) error。tickRate 表示每秒 tick 数，一个 tick 的时长等于 1 秒除以 tickRate，延迟换算成 tick 时向下取整；未调用 SetDelay 时延迟为 0，未设置瞬移阈值时按 64 判定，未设置外推上限时不允许外推。

要修：Push 拒绝 tick 为 0 的入参，实体 ID 为 0 或同一份快照里重复出现时返回 ErrInvalidEntity 且整份快照不入队；空快照也要入队，用来标记 tick 心跳；重复 tick 直接丢弃并计入丢弃数；乱序到达的快照按 tick 升序插入，Ticks 始终升序返回，缓冲区为空时 Queued 与 TickSpan 返回 0、Ticks 返回空切片；缓冲区最多保留 32 份，超出丢弃最旧的一份并计数；Sample 用客户端 tick 减去延迟换算出的 tick 数得到播放位置，取相邻两份快照插值，位置早于最早快照或缓冲区为空时返回 false；播放位置正好落在某份快照的 tick 上时直接返回该快照状态，不得标成外推；两份快照之间实体位移超过瞬移阈值时直接取较新一份的位置，不做插值；越过最新快照的部分按最近两份快照的速度外推，超过外推上限后冻结并置 stale，回到正常区间后 Stale 自动复位；返回的实体按 EntityID 升序；Sample 返回的 Tick 是参与插值的较新快照 tick，外推时取最新快照的 tick，Stale() 只反映最近一次 Sample 的结果、采样本身不改动时间线；TickSpan 返回已排队快照首尾的时长；自适应延迟下调时不得小于已排队快照的跨度，否则会立刻耗尽缓冲造成二次抖动；实体在中间某份快照里缺失时，只能用其前后都存在的那一段插值，不能跨过缺失区间连线；Snapshot 与 Restore 要保存时间线与延迟状态，恢复后播放游标仍落在原时间线上，不能因为墙钟前进把积压快照一次性快进丢弃，坏数据返回 ErrBadSnapshot 且不改动现有状态。

硬约束：Push 与 Sample 会被多个渲染线程并发调用，时间线、丢弃计数与延迟只能由同一把锁保护；32 份是硬顶，任何路径都不得越界；Sample 只在已排队快照上扫描，不得为了取一帧把整条时间线复制一遍；Restore 要么整体替换成功，要么整体失败。验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-11`
- 实现文件：`snapshot_interpolation_clock.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`InterpolationClock`、`Snapshot`、`EntityState`、`State`
