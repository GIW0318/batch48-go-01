# 出口流量分级仲裁：担保额度、空闲借用与最小回退抢占

## 题面


流量分类器是防队列挤占的边界；攻击者用大量低优先级流占满出口，就能把语音信令饿死。出口链路扩容之后，线上反而更不稳：语音网关的 EF 流在晚高峰被大量 BE 下载挤到排队，AF 流借出去的额度在流释放时被还回了错误的档位，重启一次之后有些档位的占用凭空多出一截；运维把链路容量临时下调，结果几个大流被悄悄丢掉，业务侧没有任何清单可对。

落点 gsb/batch48-go-safety-a-19 的 traffic_class_arbiter.go，类型 TrafficClassArbiter，构造函数 NewTrafficClassArbiter(ArbiterConfig) (*TrafficClassArbiter, error)，配置含 Capacity、Guarantees 与 MaxFlows，档位常量是 ClassEF、ClassAF、ClassBE。对外签名保持：Admit(flow FlowID, cls Class, demand uint64) (Decision, error)、Release(flow FlowID)、Reclass(flow FlowID, cls Class) error、Tick(now time.Time) []Preemption、SetPolicy(p ArbiterPolicy) error、SetCapacity(capacity uint64) ([]Demotion, error)、Usage(cls Class) uint64、Flows() []FlowID、Stats() ArbiterStats、Snapshot() ([]byte, error)、Restore([]byte) error。

要修的地方：三档担保之和超过链路容量必须报 ErrGuaranteeOverCapacity，容量为零、MaxFlows 非正或出现未知档位报 ErrInvalidConfig；Admit 对零需求报 ErrZeroDemand，对未知档位报 ErrInvalidClass，对重复流报 ErrFlowExists，超过 MaxFlows 报 ErrFlowLimit；授权先扣本档担保，再按 BE、AF、EF 的顺序借用别档的空闲额度，Decision 要给出实际授权量与借出档列表，Usage 按额度归属记账而不是按流所属档位；本档与可借额度都不足时进入等待队列并返回队列位置；Release 必须把借来的额度还回真正的借出档，否则后续新流会拿到不存在的额度；Reclass 在目标档额度不足时整体回滚并报 ErrClassGuaranteeFull，同档报 ErrSameClass，未知流报 ErrUnknownFlow；Tick 按优先级重试等待流，必要时选择可回退的最小流集合抢占低优先级流，被抢占流回到等待队列并保留排队位置，抢占记录要带上被抢占流、释放量、抢占者与队列位置；关闭 Borrowing 后只能使用本档剩余额度，关闭 Preemption 后 Tick 不得抢占；快照要保存每档担保、已用归属、借用关系、队列顺序与计数，恢复后 Release 不能把额度还错档；损坏快照报 ErrBadSnapshot 且不改动现有状态。同一 Tick 内既有新流申请又有旧流释放时，释放出来的额度要先回填被抢占流，再按优先级分配给新流。低优先级流连续多个 Tick 未获额度要触发老化提升，但提升不得突破高优先级担保额度。链路容量下调后已有担保之和超出新容量时，必须按配置顺序逐档收缩并给出被降档的流清单，不能静默丢弃，降档记录要能追溯到具体流与释放量。

硬约束：Admit、Release、Reclass、Tick、SetPolicy、SetCapacity 与快照共用一把互斥锁，所有决策在一个 Tick 内原子提交，读方不得看到半更新的归属表；担保之和不得超过链路容量，任何时刻各档归属之和不得超过容量；等待队列顺序与借用归属必须可重放，重启后按快照继续。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-19`
- 实现文件：`traffic_class_arbiter.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`TrafficClassArbiter`、`Class`、`FlowID`、`ArbiterConfig`、`ArbiterPolicy`
