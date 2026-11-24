# 回放分块、关键帧索引与定位

## 题面

赛后回放拖到第七分钟就黑屏，同一份录像两台机器定位不同帧，客服复现画面从半截开始。落点 gsb/batch48-go-game-a-03 的 replay_chunk_indexer.go，类型 ReplayIndexer，签名保持：NewReplayIndexer(cfg ReplayConfig) (*ReplayIndexer, error)、Append(f Frame) error、Flush() error、Seek(at time.Duration) (Cursor, error)、Locate(at time.Duration) (uint64, error)、ReadBlock(cursor Cursor) ([]Frame, error)、Verify(blockID uint64) error、Prune(keep int) (int, error)、Snapshot() ([]byte, error)、Restore([]byte) error，余下按注释保持签名。

要修：帧号从 1 起且递增，倒序报 ErrNonMonotonicTick，时间回退报 ErrTimeRegression，空载荷报 ErrEmptyPayload；块满才提交，未 Flush 的块对定位不可见，Flush 幂等；每块记起始帧、帧数、起止时间、关键帧与校验；定位给出不早于目标时刻的首帧，先找最近关键帧再顺放，早于首帧则从首帧起；ReadBlock 从游标偏移读到块尾，越界报 ErrOffsetOutOfRange，未知块报 ErrBlockNotFound；Verify 重算校验，坏块报 ErrBlockCorrupt；块数超上限或 Prune 淘汰最旧并返回淘汰数，keep 非正报 ErrInvalidKeep；关键帧间隔超上限仍要定位正确，只能从更早关键帧顺放；淘汰后绝对帧号不得重编号，定位保持单调；坏块被跳过时读取要留缺口标记，不得静默拼连续回放；快照带已提交块与未完块，重启可续写，坏快照报 ErrBadSnapshot。

硬约束：写入与索引更新有明确可见次序，读方只见已提交块；读写并发不得撕裂块内容；块数、关键帧表与快照大小设上限。

验收：go test ./... 15 条全过，测试不得修改、跳过或写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-03`
- 实现文件：`replay_chunk_indexer.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ReplayIndexer`
