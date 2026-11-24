# GPU 资源批量加载事务的全成或全回滚

## 题面

美术同学切场景时经常遇到显存越用越多：一次批量加载里有张贴图上传失败，界面已经报「资源加载失败」，但此前建好的顶点缓冲和纹理没有释放，再进一次场景又叠一份；有时整批资源都成功了，回滚却按资源名排序释放，先建的后释放，引用计数对不上，另一个还在共用同一张纹理的场景被连带清空。落点 gsb/batch48-go-game-b-05 的 gpu_resource_load_tx.go，类型 ResourceLoadTx，签名保持：NewResourceLoadTx(up Uploader, lim Limits) *ResourceLoadTx、Begin(sceneID string) (TxID, error)、Stage(txID string, r Resource) error、Commit(txID string) error、Rollback(txID string) (int, error)、Cancel(txID string) error、Progress(txID string) float64、State(txID string) (TxState, error)、ActiveTx() int、RefCount(key string) int、Reclaim(now time.Time) (int, error)、Snapshot() ([]byte, error)、Restore(payload []byte) error。Resource 用 Key、Kind、Bytes 描述一个按内容键寻址的资源；Limits 给出 MaxTx、MaxPerTx、MaxKeys 与 Lease。

要修：同一场景并发调用 Begin 会拿到两个独立事务，两边各自提交后场景资源重复；空场景号、缺失上传器、MaxTx 或 MaxPerTx 为零时仍能开事务；单事务资源数、全局内容键数、同时打开的事务数都可以无限增长；空内容键或字节数为零的资源被接受；同一事务内重复暂存同一内容键时上传了两遍；同一内容键配不同 Kind 或字节数被当成同一资源静默通过；上传失败后事务仍可继续暂存并提交，已建资源没有回到加载前状态；回滚只清掉一半资源，返回的数量与实际释放数不符，第二次回滚不报错；两个场景共用同一内容键时上传两次，引用计数只记一份，先回滚的那个把仍在使用的资源释放掉；对已提交事务执行取消时把已发布资源也释放了；Progress 在提交后仍为 0，未知事务返回 0 而不是 -1；进程退出后未提交事务占用的资源无法被巡检回收，租约过期的判定时松时紧，还会误伤已提交事务；快照没有带上引用计数和事务状态，恢复失败时把现有状态改坏了一半；Lease 设成零或负数不报错。

硬约束：Begin、Stage、Commit、Rollback、Cancel、Reclaim 在同一把锁下串行，同一内容键的首次上传与引用计数增减必须原子，并发暂存不能出现两次上传或丢计数；事务资源在提交前可全量释放，回滚与取消必须按创建逆序逐个处理，共享键只在最后一个引用消失时真正释放；快照要覆盖已提交与未提交事务、每个内容键的引用数，恢复要么整体生效要么完全不生效，版本不符或内容残缺一律拒绝并保留旧状态；事务数、单事务资源数、全局键数、租约四项上限必须硬性生效，超限报对应错误而不是部分写入；重复暂存同一内容键只有第一次产生上传，后续调用按幂等处理。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-05`
- 实现文件：`gpu_resource_load_tx.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ResourceLoadTx`、`Resource`、`Limits`、`Uploader`、`TxState`
