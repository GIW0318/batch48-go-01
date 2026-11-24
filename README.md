# TLS 证书压缩协商、解压预算与字典缓存治理

## 题面

TLS 握手侧的证书压缩上线后，运维在三个机房各抓到一类问题：一个客户端先按 zlib 协商成功，随后又发了一次带回退算法的协商，服务端把字典换掉，之前缓存的解压上下文全部作废，握手成功率掉到六成；某次压缩包解压后膨胀了几百倍，进程先把明文整块分配出来才判断超限，单个 goroutine 直接把内存顶到上限；连接关闭后复用同一张会话票据重新建连，新连接竟然沿用了旧连接协商出的算法，双方对压缩格式的理解不一致，证书校验随机失败。回放日志还显示：同一 zlib 算法被两条连接重复创建字典，字典缓存条数远超配置上限，最久未用的条目一直没有被淘汰；连接数达到上限后，回退不压缩的新连接也被算进额度，真正需要压缩的握手被拒之门外；同一算法并发握手时还能看到两个字典同时被创建，引用计数对外不可见。落点 gsb/batch48-go-safety-a-27 的 cert_compression_cache.go，类型 CertCompressionCache，签名保持：NewCertCompressionCache(maxConns, maxDicts int) *CertCompressionCache、Negotiate(connID string, offered []Algorithm) (Algorithm, bool, error)、Negotiated(connID string) (Algorithm, bool)、Decompress(connID string, compressed []byte) ([]byte, error)、Reset(connID string) error、SetLimits(maxOut, maxRatio int) error、DictionaryID(connID string) string、Stats() CacheStats、Snapshot() ([]byte, error)、Restore(data []byte) error。算法常量至少包含 AlgorithmNone、AlgorithmZlib、AlgorithmDeflate、AlgorithmBrotli，服务端只实现 zlib 与 deflate。

要修：空连接号报 ErrBlankConn；每连接只保留首次协商成功的算法，重复协商报 ErrAlreadyNegotiated 且不得替换首次结果；offered 里一个可实现的压缩算法都没有时回退 AlgorithmNone 并返回 false，这条连接不占连接额度，也不建立字典；活跃连接数超过 maxConns 报 ErrConnLimit，Reset 之后额度要释放；未协商连接或回退连接调用 Decompress 分别报 ErrNotNegotiated 与 ErrNoCompression；解压输出超过 maxOut 报 ErrOutputTooLarge，压缩比超过 maxRatio 报 ErrRatioExceeded，两者都必须在解压过程中提前中止，返回的明文长度为零，不能先整块分配；压缩数据损坏报 ErrCorruptCompressed；SetLimits 对 maxOut 小于等于零、maxRatio 小于一报 ErrBadLimits；字典按算法共享，同一算法的连接拿到同一个 DictionaryID，字典缓存条数超过 maxDicts 时按 LRU 淘汰最久未用的条目；Stats 报告的连接数、字典数与实际表项一致，回退连接不得计入活跃连接；快照只保存活跃连接的算法与字典标识，恢复后未出现在快照里的连接必须重新协商；连接关闭后按同一 ticket 新建的连接必须从零开始协商，不得沿用旧连接的算法或字典；Restore 对空数据与损坏内容报 ErrBadSnapshot。

硬约束：Negotiate、Decompress、Reset、SetLimits 与快照操作共用互斥锁，字典缓存的命中、引用与 LRU 顺序更新必须原子，多个握手 goroutine 并发解压同一连接时不得读到半初始化的字典；解压预算在读取过程中逐块检查，不得先解压完整结果再判断；快照与恢复要么整体生效要么完全不生效。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-27`
- 实现文件：`cert_compression_cache.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`CertCompressionCache`
