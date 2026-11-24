# 帧同步战斗的推进、缺帧降级与追帧

## 题面

跨服联赛第三场，十个客户端开局四十秒后漂移，观战端看到角色卡墙里，重连玩家按本地帧继续跑，比分与录像不符。落点 gsb/batch48-go-game-a-02 的 frame_sync_state_machine.go，类型 FrameSyncMachine，签名保持：NewFrameSyncMachine(players []string, cfg FrameSyncConfig) (*FrameSyncMachine, error)、Submit(playerID string, frame uint64, in Input) error、Tick(now time.Time) (TickResult, error)、WaitDepth() int、CatchUp(maxFrames int) (int, error)、Pause() error、Snapshot() ([]byte, error)、Restore([]byte) error，余下按注释保持签名。

要修：帧号从 1 起，0 与名单外玩家报 ErrInvalidFrame、ErrUnknownPlayer，同玩家同帧重复提交报 ErrDuplicateInput；一帧收齐全部输入才推进，缺人只报等待，WaitDepth 统计卡住的帧；一次 Tick 最多推 CatchUpPerTick 帧；缺帧超过 MaxWaitFrames 降级跳过，标记 Degraded；暂停不推进，重复暂停与未暂停恢复分别报 ErrAlreadyPaused、ErrNotPaused；CatchUp 只消化齐帧，预算非正报 ErrInvalidBudget，暂停中报 ErrPaused；每帧产出可复算校验和，只读已推进帧；到达顺序与 Tick 节奏不同时校验和必须一致；暂停期间输入缓存在原帧号，恢复后按原帧推进；预算用尽停在安全帧并上报落后量，不得跳帧；降级跳过后迟到的输入不得把跳过的帧重新补播；快照带当前帧、校验与未消费输入，重启后同帧续跑并得相同校验和，坏快照报 ErrBadSnapshot。

硬约束：并发提交同玩家同帧只留一份；推进与快照互斥；校验和只由输入决定，不依赖 map 遍历。

验收：go test ./... 15 条全过，测试不得修改、跳过或写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-02`
- 实现文件：`frame_sync_state_machine.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`FrameSyncMachine`
