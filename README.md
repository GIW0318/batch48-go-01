# 赛季阶段推进、写入归属与结算

## 题面

赛季切换当晚，结算列表混进上一阶段积分，重复点结算又出第二份清单，两份各发了一次补偿。落点 gsb/batch48-go-game-a-04 的 season_phase_controller.go，类型 SeasonPhaseController，签名保持：NewSeasonPhaseController(maxPhases, maxSettlements int) (*SeasonPhaseController, error)、EnterPhase(seasonID, phase string, at time.Time) error、Submit(seasonID, subject string, v Value, at time.Time) error、Settle(seasonID string) (Settlement, error)、Reopen(seasonID string, at time.Time) error、Restore([]byte) error，余下按注释保持签名。

要修：赛季或阶段名为空报 ErrInvalidSeason、ErrInvalidPhase，重名报 ErrPhaseExists；迁移时间严格向前，回退或相等报 ErrTimeRegression，阶段超限报 ErrPhaseLimit；写入按时间戳落区间，早于首阶段报 ErrBeforeSeasonStart，区间外报 ErrOutOfPhase；空主体与负值报 ErrInvalidSubject、ErrInvalidValue；结算当前阶段，按主体稳定排序，同一阶段只出一份，重复结算返回同一份并标 Duplicate；结算超限报 ErrSettlementLimit；未结算重开报 ErrNotSettled，重开早于结算报 ErrTimeRegression；结算后到达的旧阶段写入要拒绝并记录，不得顺延进新阶段；重开不回收已结算奖励，新写入须落在重开后区间；迁移失败停在原阶段并保留已提交边界，不得留两套边界；快照带阶段表、边界、条目与结算，重启后一致。

硬约束：并发写入按主体原子累加不丢条；迁移、结算与快照互斥；阶段表与结算记录设上限。

验收：go test ./... 15 条全过，测试不得修改、跳过或写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-04`
- 实现文件：`season_phase_controller.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SeasonPhaseController`
