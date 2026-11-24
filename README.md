# 审计哈希链封存、带签检查点与裁剪后可校验

## 题面

审计流水按条封存成哈希链，定期打带签检查点，旧记录按保留窗口裁剪，复核任务只拿到剩余区间。上线后发现：裁剪过的段被整段判失效，检查点序号与摘要对不上，重启后重复写入同一条序号还会被静默覆盖，出了事故反而说不清哪一段被动过。审计数据要保留一段时间供复核，超过窗口的记录按天裁剪，裁剪不能影响剩余区间的可信度。要修 gsb/batch48-go-safety-b-08 的 audit_chain_sealer.go，类型 AuditChainSealer，签名：NewAuditChainSealer(Options) *AuditChainSealer、Append(rec Record) (Sealed, error)、Checkpoint() (Checkpoint, error)、Verify(from, to uint64) (Report, error)、Truncate(before uint64) error、Restore([]byte) error。

边界要求：
① 序号从 1 起逐条加一，跳号、回退或重启后重复同一序号报 ErrSeqConflict，链头不动，已封存记录也不能被替换。
② 空租户或空动作报 ErrBadRecord，链头不动，校验失败的记录不占序号。
③ 首条前驱为全零摘要，之后 Prev 必须等于前一条摘要，Head 与 Append 返回一致，不能各算各的。
④ 摘要是 SHA-256，输入依次为前摘要、序号 8 字节大端、租户、动作、负载，段间以 0x00 分隔，编码顺序不能调换。
⑤ 保留条数超过 MaxRecords 报 ErrRecordLimit，已封存记录不被挤掉，链头也不前进。
⑥ 检查点用 Key 做 HMAC-SHA-256，签名不符或摘要被改报 ErrBadCheckpoint，校验失败不得写入检查点表。
⑦ 检查点超过 MaxCheckpoints 淘汰最旧，Checkpoints 按创建顺序返回，淘汰后剩余顺序不变。
⑧ Verify 的 from 必须不小于 1、from 不大于 to、to 不超过链头，否则报 ErrRangeInvalid，不能静默截断到合法范围。
⑨ Verify 逐条重算，摘要不符时 Valid 为假、Reason 为 digest_mismatch；完整未动的范围 Checked 等于区间长度、Gaps 为空，Report 的 From 与 To 回填请求区间。
⑩ Truncate 只丢弃 before 之前的记录，保留 before 及之后；截断点超过链头报 ErrTruncateAhead，链头与保留记录都不动。
⑪ 裁剪后 Verify 要从剩余第一条起与前驱检查点对齐，不能因为缺前驱把整链判失效，也不能把缺失序号当篡改。
⑫ 快照保存链头、保留记录与检查点，恢复后 Head 能续接；坏快照报 ErrBadSnapshot 且不动现网，整链复核留给 Verify。

常见错法：裁剪后从剩余第一条重新起链，把前驱当成全零；检查点先发布再算签名，并发 Append 时序号与摘要错位；Verify 只比最后一条摘要，中间被改也看不出来。

硬约束：并发上，Append 按序号串行，检查点与链头原子发布，Head 与记录成对，不得出现链头已前进但记录还没落的情况。容量上，记录数与检查点数都是硬顶。恢复上，恢复后重复 Append 同一序号报冲突而不是覆盖。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死输入；除跑通用例，还要自己补并发与重启场景：Append 与 Checkpoint 交叉、裁剪后复核、快照恢复后继续追加、裁剪后再追加新记录能续接；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-08`
- 实现文件：`audit_chain_sealer.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`AuditChainSealer`、`Options`、`Record`、`Sealed`、`Checkpoint`、`Report`
