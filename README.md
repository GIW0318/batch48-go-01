# 段位晋级赛的系列状态机与赛季软重置

## 题面

新赛季第一天客服接到两类工单：晋级的玩家段位没动，保级的玩家反而掉了一档；另一名玩家的晋级赛打了四局，同一场对局被前后记了两次。运营值班还发现，赛季软重置脚本被重跑了一次，同一批玩家的分数被连扣两轮；有玩家中途退赛换了人，旧系列却仍在结算，把新赛季的定级一起带偏。这一版要把晋级赛的进度、逐局记账与赛季重置收进同一个账本，结算只能落在开赛时冻结的那一档上。落点 gsb/batch48-go-game-a-07 的 rank_promotion_series.go，类型 PromotionSeries，签名保持：NewPromotionSeries() *PromotionSeries、Register(playerID, tier string) error、OpenSeries(playerID, seasonID string) (Series, error)、RecordGame(seriesID, matchID string, win bool) (Series, error)、RecordDisconnect(seriesID, matchID string) (Series, error)、Resolve(seriesID string) (Outcome, error)、Abort(seriesID, reason string) error、SoftReset(seasonID string, ratings map[string]int) (int, error)、Series(seriesID string) (Series, bool)、SeriesGames(seriesID string) []GameRecord、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：Register 只认 bronze 到 diamond 五档，其他报 ErrUnknownTier，初始分按档位基线写入，登记后 Tier 与 Rating 能查回同一档位与基线分；OpenSeries 要求玩家已登记，未登记报 ErrPlayerNotRegistered，开局返回 State 为 SeriesRunning、胜负局均为 0、玩家与赛季绑定正确的系列，并把开局时的段位冻结进系列；三局两胜，两胜或两负即完成，状态转 SeriesComplete，完成后继续记对局报 ErrSeriesClosed；同一系列同一对局号只记一次账，重复报 ErrDuplicateMatch，胜负与局数都不变，不同对局号按到达顺序编号，SeriesGames 按记账顺序返回对局号、胜负与 forfeit 标记，重复调用结果一致且不改变进度；结算两胜晋级一档、一胜两负保级、两负降一档，越界按阶梯上下端封顶，Outcome 同时给出旧档、新档、胜负局数，Tier 随结算更新；Resolve 在未完成时报 ErrSeriesIncomplete 且段位不动，已结算再报 ErrAlreadyResolved，段位只变动一次；掉线判负按判负计入局数，并在对局记录里以 forfeit 标记与真实对局区分；Abort 只中止进行中的系列，状态转 SeriesAborted，之后记对局与结算都报 ErrSeriesAborted，已完成系列报 ErrSeriesClosed，中止不改变段位；软重置把传入结算分下调 200 分再换算段位并返回受影响人数，Rating 与 Tier 同步更新，同一赛季重复执行必须幂等，不能第二次再压；系列进行中玩家段位被外部改动时，结算以系列开始时冻结的段位为准；快照保存系列进度、对局记录、段位、分数与赛季重置标记，重启后能继续记对局与结算，恢复后的 Tier 与 Rating 保持一致，gold 的基线分为 1400，坏快照报 ErrBadSnapshot。

硬约束：同一系列并发结算只能产生一个结果，其余必须拿到 ErrAlreadyResolved；软重置可重放，重复执行不改段位与分数；快照与写入互斥，恢复后不得出现半个系列或缺失的对局记录；系列、对局记录与赛季标记设硬顶，超出按错误返回而不是覆盖已有条目。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发结算、软重置重放与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-07`
- 实现文件：`rank_promotion_series.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`PromotionSeries`
