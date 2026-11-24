# 对局中途补位的候选筛选、席位占位与结算隔离

## 题面

一场已经进行到尾声的对局仍被塞进两名补位玩家，进场十秒就结算，奖励还发了两份；另一个席位被候选占住之后一直空着，直到对局结束都没人补上。数据组复盘又看到：补位战绩被混进主名单，MVP 统计翻倍；占位超时的候选没有回到池子，下一场再也筛不到他；还有跨区候选被硬拉进来，延迟高到整局卡顿。这一版要把候选池、按对局的占位和席位绑定拆开管，补位只在同区域、剩余时间足够的对局里发生，占位失败要能干净回滚。落点 gsb/batch48-go-game-a-09 的 match_backfill_guard.go，类型 BackfillGuard，签名保持：NewBackfillGuard(poolCap int, minRemaining, claimTTL time.Duration) *BackfillGuard、OpenMatch(matchID, region string, basePlayers int, remaining time.Duration) error、Register(candidateID, region string, rating int, latencyMS int) error、Pick(matchID string, need int) ([]Candidate, error)、Claim(matchID, candidateID string) (bool, error)、Attach(matchID, candidateID, seatID string) error、Abort(matchID, candidateID string) error、Remaining(matchID string) time.Duration、Claimed(matchID string) []Candidate、Seats(matchID string) map[string]string、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：Register 校验候选 ID 与区域非空、评分非负、延迟为正，重复登记报 ErrCandidateExists，候选池按 poolCap 封顶，超出报 ErrPoolFull，被拒绝的登记不得改动池子；OpenMatch 记录对局区域、基础人数与剩余时间，对局号为空报 ErrInvalidMatchID，剩余时间为负报 ErrInvalidRemaining，重复打开按最新一次覆盖；筛选与占位都必须先打开对局，未登记的对局报 ErrMatchUnknown；Pick 只挑同区域空闲候选，按延迟升序、同延迟按评分降序、再按 ID 升序，最多返回 need 个，候选不足时返回现有数量，need 非正报 ErrInvalidNeed，Pick 只读不改变候选状态；Claim 原子抢占，同一候选并发只有一人成功并拿到 true，其余拿到 false 而不是错误，占位成功后候选状态转 CandidateClaimed 并记录所属对局，该候选从池中移出，别的对局不再筛到；Attach 只接受已占位候选，未占位报 ErrNotClaimed，一个席位只能挂一名候选，重复挂报 ErrSeatTaken，成功后候选状态转 CandidateAttached 并记录 seatID，Seats 能查回席位与候选的对应；Abort 回滚占位，候选回到 CandidateIdle、清空对局归属并能被重新筛选；对局剩余时间低于 minRemaining 时补位整体拒绝并报 ErrBackfillClosed，即使候选已经占位也要撤回；补位玩家的胜负与奖励单独统计，通过 RecordResult 与 BackfillResults 查询，但人数要计入对局总数并由 Headcount 反映；候选占位后放弃时不得让席位长期空置，要按 claimTTL 超时释放并允许重新筛选，ExpireClaims 返回本轮被释放的候选，已入局的候选不得被踢回池子；Claimed 按候选 ID 升序返回当前占位列表；快照保存候选状态、占位截止与席位绑定，重启后已占位未入局的候选能恢复且不被重复推荐，坏快照报 ErrBadSnapshot 且不改动现有状态。

硬约束：同一候选并发占位只能有一个赢家，占位失败必须把候选状态回滚，不能留下半占位；重启恢复与超时释放互斥，释放不会把已入局的候选踢回池子；候选池、席位表与占位记录设硬顶，超出按错误返回而不是覆盖；所有占位截止判断走同一可替换时钟，注入时钟后结果必须可复现。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发占位与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-09`
- 实现文件：`match_backfill_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`BackfillGuard`
