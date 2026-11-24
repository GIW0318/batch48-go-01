# 共享内存文件的封印组合

## 题面

共享内存通道上线后，两个进程约定同一块区域：一个只读映射，一个负责写。运维发现只读端的数据还是被改掉了，事后核查是有人先用截断把文件缩小、再追加写回，绕过了之前下过的禁止改写标记；更麻烦的是容器重启后只读文件变回可写，下游直接覆盖了指标快照，两个进程对同一块内存的认知彻底分叉。两个进程的映射地址都没变，只有封印标记像被谁悄悄擦掉；只读端的进程一直以为自己拿到的是封印过的只读视图，直到指标被覆盖才有人怀疑封印根本没有落进持久化；两块内存的校验和对不上之后，链路两侧还各自重启过一次，问题反而更难复现。落点 gsb/batch48-go-safety-b-13 的 file_seal_guard.go，类型 FileSealGuard，维护一组共享内存文件的封印状态。接口保持：NewFileSealGuard(Options) *FileSealGuard、Create(name string, size int) (*Sealed, error)、Seal(name string, flags SealFlags) error、Seals(name string) (SealFlags, error)、Write(name string, off int, data []byte) error、Truncate(name string, size int) error、Release(name string) error、Snapshot() ([]byte, error)、Restore([]byte) error、Size(name string) (int, error)、Contents(name string) ([]byte, error)。封印位为 SealNoWrite、SealNoGrow、SealNoShrink、SealNoAppend。

要修的行为如下。文件名为空、大小为负或超过上限、重名、数量到顶，分别报 ErrInvalidName、ErrInvalidSize、ErrNameTaken、ErrFileLimit，新文件的封印为空且内容全为零。写入偏移小于零或大于文件长度报 ErrWriteRange；偏移落在文件内时原地覆盖且长度不变；偏移等于文件长度时按追加处理并增长；写入空数据按无操作处理，不改长度也不动封印。封印只增不减，后一次按位或进去，未知封印位报 ErrSealBits 且不改动已有标记。禁止改写生效后覆盖写被拒，内容一个字节都不变；禁止增长生效后放大截断被拒，追加也被拒；禁止收缩生效后缩小截断被拒；没有封印时截断可以自由放大缩小，放大区域补零，截断到当前长度按无操作处理；释放后文件不可再读也不可再写，释放不存在的文件报 ErrNoSuchFile。先加禁止增长再加允许追加时，追加仍必须被拒绝，组合要按最严语义而不是后写覆盖；封印后被映射的句柄发起写入要整体报错且文件内容不变，不能写进去一半才失败；Restore 后封印标志必须原样恢复，只恢复名字与大小而丢封印等于把只读文件变回可写。

硬约束：每个文件一把锁，Write 与 Truncate 走同一把锁串行，Seals 只读不被阻塞；封印标记与内容一起落进快照，恢复后对同一文件的所有句柄看到同一份标记；文件数量与单文件大小都有硬顶。

验收：go test ./... 的 15 条用例全部通过，测试文件不改、不跳过、不按固定输入写死；并发写与重启恢复自行构造验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-13`
- 实现文件：`file_seal_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`FileSealGuard`、`Sealed`、`Options`、`SealFlags`
