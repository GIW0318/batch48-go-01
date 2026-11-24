# 沙箱临时文件独占创建、租户配额与孤儿回收

## 题面

沙箱任务把中间结果写到临时目录，多个任务互相覆盖同名文件，跑完也不清理，磁盘被孤儿文件占满；有一次父目录被换成指向外部的符号链接，文件直接落到了沙箱外。临时目录由平台统一发放，每个租户一份，文件名必须独占，配额按租户结算。要修 gsb/batch48-go-safety-b-09 的 secure_temp_guard.go，类型 SecureTempGuard，签名：NewSecureTempGuard(Options) *SecureTempGuard、NewFile(tenant, purpose string, n int) (*TempFile, error)、NewDir(tenant string) (*TempDir, error)、Release(id string) error、Sweep(now int64) (int, error)、Snapshot() ([]byte, error)。

边界要求：
① 未配置根时创建报 ErrBadRoot；空租户或空用途报 ErrBadArg，非法参数不产生条目。
② 同租户重复建目录报 ErrPathExists；未建租户目录就建文件报 ErrNoParent，不会自动补建目录。
③ 长度非正或超过 MaxFileBytes 报 ErrBadSize，边界值本身按允许处理。
④ 名字带随机后缀不重名；文件模式 0600、目录模式 0700；路径必须落在租户目录下，不能拼到根外。
⑤ 条目数或字节数超过每租户上限报 ErrQuotaExceeded，不留半创建的空文件，配额一次结算到位。
⑥ Write 增长同时受单文件上限与租户字节上限约束，失败时不改文件大小，已用字节也不变。
⑦ Release 未知或已释放的条目报 ErrNoEntry；释放后条目仍计入配额，直到被回收，句柄数先减。
⑧ Sweep 按 TTL 回收已释放文件与到期空目录并返回条数；有活跃句柄的跳过并顺延到期时间，正在写入的不能删。
⑨ Usage 返回条目、字节与活跃句柄；未知租户给零值，三个数字要能互相对上。
⑩ 快照保存根、命名序列与全部条目，恢复后已发出的 id 不重复分配；坏快照报 ErrBadSnapshot 且不动现网。
⑪ 父目录检查后被换成外部符号链接时，写入必须失败，外部目录不得落文件，检查与创建之间不能留空档。
⑫ 并发创建到条目上限时只能成功到上限个，失败方不留半创建空文件，Usage 的条目数与成功数一致。

易漏的三点：父目录检查后被换成外部符号链接，写入必须失败且外部不落文件；并发创建到上限时只能成功到上限个，失败方不留空文件；Sweep 只回收超期且无活跃句柄的条目，正在写入的跳过并顺延到期时间。

硬约束：并发上，创建名额一次结算到位，先占额度再落盘，Usage 与 Sweep 并发安全。容量上，每租户条目数、每租户字节数、单文件字节数都是硬顶。恢复上，Restore 一次性替换状态，恢复后的配额占用与快照一致。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死输入；除跑通用例，还要自己补并发与重启场景：并发建文件到上限、Sweep 与 Write 交叉、快照恢复后继续按原 id 写入；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-09`
- 实现文件：`secure_temp_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SecureTempGuard`、`Options`、`TempFile`、`TempDir`、`Usage`
