# 任务包 batch48-go-game-a-24

## 模块

- module: gsb/batch48-go-game-a-24
- package: task
- file: parry_chain_guard.go

## 题面

竞技场结算接连出怪事：同一次攻击被同时记成招架和格挡，减伤只按一半算；暴击伤害有时比不暴击还低；背刺位绕到身后打上去，对方照样闪避；无敌帧里明明应该免疫，血量却照掉。战斗日志按批次回放两次，同一毫秒同一目标的多次命中顺序不同，命中总数也跟着变，客服拿着两份回放无法解释。还有玩家反馈，同一目标先被招架又被格挡时，两段减伤叠在一起，实际承伤比面板低一大截。落点 gsb/batch48-go-game-a-24 的 parry_chain_guard.go，类型 ParryChainGuard，签名：NewParryChainGuard(Options) *ParryChainGuard、SetModifiers(string, Modifiers) error、Resolve(HitRequest) (Outcome, error)、Batch([]HitRequest) ([]Outcome, error)、Immune(string, int64) error、ChainOrder() []string、Explain(Outcome) string、ImmuneUntil(string) (int64, bool)、Stats() Stats、ResetStats()、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、空目标、伤害非正、任一判定值不在 0 到 99，Resolve 报 ErrInvalidRequest；修正值越界（闪避、招架、格挡超过一百，命中加成低于负一百）报 ErrInvalidModifiers。
二、判定链固定为免疫、命中、闪避、招架、格挡、暴击，ChainOrder 按这个顺序返回，短路时只给唯一原因码，原因码与 Explain 的输出一致。
三、无敌窗口内判免疫、伤害为零；窗口到期的那一刻恢复常规判定；ImmuneUntil 能查到截止毫秒。
四、命中判定值达到 HitChancePct 即判未命中（大于等于即失败），未命中伤害为零。
五、闪避成立时伤害为零并终止后续判定。
六、招架只保留 ParryKeepPct 的伤害，Blocked 为 false，且不再继续判格挡与暴击。
七、格挡按 BlockReducePct 减伤后仍继续判暴击，结果标记 Blocked 为 true。
八、暴击按减伤之后的最终伤害乘 CritMultPct 取值，先暴击后减伤与先减伤后暴击只能取后者一种。
九、背刺拦掉闪避、招架与格挡，但仍要过命中判定，正常落地命中。
十、命中伤害不低于 Options.MinDamage；Batch 的结果与请求一一对应、按请求顺序返回，Stats 的 Total、Hits、Misses、Dodges、Parries、Blocks、Crits、Immunes 逐类计数，暴击在 Hits 与 Crits 中各记一次，ResetStats 清零。
十一、快照往返修正值、免疫窗口与统计计数，恢复后同一请求得到与恢复前一致的原因码；坏快照报 ErrBadSnapshot，现有状态不被改动。
十二、同一目标同一毫秒的多次命中在 Batch 中按请求序号稳定结算，结算次序固定，不随 map 遍历变化。

硬约束（并发与原子性）：判定只使用请求数值与已登记修正，不读随机源，同一输入必须得到同一结果；Resolve、Batch、Immune 并发执行时同锁保护，统计计数不得丢条或重复计数，任意时刻 Total 等于已结算的请求数。

硬约束（快照与重启）：快照只允许看到全量旧状态或全量新状态，不存在半份统计；恢复后统计与免疫窗口继续有效，重启不得把已发生的计数清零。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死；并发与重启自验；本地实现。

## 运行方式

```bash
go test ./...
```
