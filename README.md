# batch48-go-safety-c-06

- 模块路径：`gsb/batch48-go-safety-c-06`
- 包名：`task`
- 实现文件：`alert_false_positive_suppressor.go`
- 测试目录：`tests/`

## 题面

告警平台的值班同学反映，同一条外网暴露告警在五分钟里会被派发几十次：规则号、主机、端口完全一样，只是告警编号和等级不同，却每次都新开一张工单。运维加了一批临时白名单想压住噪声，结果白名单过期那几天，原本该重新冒出来的告警一条都没有，日报里完全看不到。上周基线库整体迁移到新版本，旧基线打出来的告警继续按老口径判重，新基线下的第一次命中反而被当成重复吞掉，抑制计数也对不上。

请修 gsb/batch48-go-safety-c-06 模块里的 alert_false_positive_suppressor.go，类型名为 AlertSuppressor，包名 task，只用标准库。接口签名保持现状：NewAlertSuppressor(SuppressorOptions) *AlertSuppressor、Fingerprint(Alert) string、Suppress(Alert, time.Time) (bool, Reason)、AddException(Exception) error、RemoveException(string) error、Exceptions() []Exception、Count(string) int、SetBaseline(string, int) error、Baseline(string) int、Recount() error、Stats() SuppressorStats、Reset() error、Snapshot() ([]byte, error)、Restore([]byte) error。Alert 含 ID、RuleID、Target、Port、Baseline、Severity；Exception 含 Fingerprint、Expires、Note；SuppressorOptions 含 Window、MaxExceptions、MaxFingerprints；SuppressorStats 含 Emitted、Suppressed、Evicted、Exceptions、Fingerprints。

需要修的行为：

一、指纹只由规则号、目标、端口决定：规则号先去首尾空白再转小写，目标去首尾空白，拼成“规则号|目标|端口”，告警编号与等级不参与。目标资产改名后 Target 变化，指纹随之变化，按新告警处理。

二、同一指纹在 Window 内第二次出现起一律抑制，reason 为 duplicate，首次放行计数为一，之后每次抑制计数加一；窗口从上一次放行时刻起算，超过窗口再次出现按放行处理并把计数重置为一。

三、命中未过期白名单直接抑制，reason 为 exception，且不改动该指纹的窗口计数。

四、白名单条目 Fingerprint 为空返回 ErrInvalidException，删除不存在的条目返回同一个错误；Expires 为零值表示长期有效。

五、白名单表超过 MaxExceptions 返回 ErrExceptionLimit；过期的条目在每次判定与新条目写入前先清理，不占容量；到期后该指纹重新按窗口判定，到期后的第一条告警重新放行，不沿用旧窗口。Exceptions 按到期时刻升序返回副本。

六、每个目标有独立基线版本，SetBaseline 目标为空或版本为负返回 ErrInvalidTarget；未设置过的目标 Baseline 返回 0。

七、告警携带的 Baseline 与目标当前基线不一致时按放行处理，reason 为 baseline-shift，并把该指纹的窗口计数重置为 1。

八、Recount 重新核对全部计数：目标基线已经变化的指纹计数清零，返回清理后的统计；反复调用结果不变。

九、抑制表最多保留 MaxFingerprints 个指纹，新指纹进来时淘汰窗口最早结束的那一个，Evicted 加一并留痕；被淘汰的指纹再次出现按放行处理。

十、Window 为零或负按五分钟，MaxExceptions 为零或负按 128，MaxFingerprints 为零或负按 4096。

十一、Stats 返回放行、抑制、淘汰、当前白名单条数与当前指纹数的快照，任意时刻与实际记账一致。

十二、Snapshot 要覆盖窗口起点、计数、基线、白名单与淘汰计数，Restore 到同配置的新实例后计数与统计一致，坏字节返回 ErrBadSnapshot。

硬约束：Suppress 与 AddException 由同一把锁串行，并发下同一指纹只能放行一次，其余全部按 duplicate 抑制；快照必须是自洽的整体，不能出现计数与窗口起点对不上的中间态；容量淘汰必须按最早结束时间而不是插入顺序，同一时刻结束的按指纹字典序。

验收标准：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
