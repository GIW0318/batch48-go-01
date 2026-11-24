# 一次性文件描述符交接与 nonce 消耗

## 题面

帮助进程先用高权限打开文件描述符，再通过域套接字把 fd 交给已经降权的子进程，一次请求只允许交接一次。交接发生在请求转发链路上，助手进程和业务子进程分属不同 uid，fd 一旦交出去就无法收回。排障时发现同一请求可以被取走两份，撤销之后旧请求仍然能读，pid 复用的新进程还能顶替原进程领走描述符，交接表在重启后整张丢失，线上甚至出现过同一份上传内容被两个消费者同时读到。要修 gsb/batch48-go-safety-b-02 的 helper_handoff_guard.go，类型 HelperHandoffGuard，接口为 NewHelperHandoffGuard(Options) *HelperHandoffGuard、Offer(PeerCred, []int, Meta) (string, error)、Accept(string, PeerCred) (Handoff, error)、Revoke(string) error、Snapshot() ([]byte, error)、Restore([]byte) error。

边界要求：
① Offer 校验 pid、uid、gid 与启动时刻，缺项报 ErrPeerInvalid，与 AllowedUID、AllowedGID 不符报 ErrPeerMismatch；校验失败不得留下任何未决条目。
② 空 fd 列表报 ErrNoFDs，超过 MaxFDsPerRequest 报 ErrTooManyFDs，负值或同一列表内重复报 ErrInvalidFD，被拒请求不占用名额。
③ 未决数达到 MaxPending 报 ErrPendingFull，原表与计数保持不变，已经排队的请求不受影响。
④ nonce 为 32 位小写十六进制；Pending 返回未决数，Inspect 查看单条未决详情且不消耗，返回的 fd 顺序要与提交时一致。
⑤ Accept 对同一 nonce 只成功一次，重取报 ErrNonceConsumed，未知 nonce 报 ErrUnknownNonce；成功之后未决数减一。
⑥ nonce 超过 NonceTTL 报 ErrNonceExpired，并从表中摘除，Pending 相应减一；过期判断以注入的 Now 为准，不能读系统时钟。
⑦ Accept 的 peer 与 Offer 不符报 ErrPeerMismatch，请求保留，原 peer 稍后仍可取走，不能因为一次错认就把请求作废。
⑧ Revoke 未知或重复撤销报 ErrUnknownNonce，撤销后 Accept 只能得到 ErrUnknownNonce，不能再把描述符交出去。
⑨ 摘要为 method、path、body 以 NUL 分隔后做 SHA-256 的小写十六进制；Handoff 原样带回 fd 与 meta，字段与顺序都不得改变。
⑩ Stats 分别累计 offer、accept、revoke；快照保存未决表，恢复后 nonce 仍可按原 peer 取走，摘要与到期时间不变，坏快照报 ErrBadSnapshot 且不动现状。
⑪ pid 复用必须连启动时刻一起比较，只看 pid 不得放行；同 pid 但启动时刻不同要当成另一个进程。
⑫ 过期、撤销、接受三种出表路径都不能让描述符被第二次交出，同一 nonce 在表里最多存在一份。

这里最容易出错的是把查表与摘表分成两步，两个并发 Accept 都查到条目再各自返回；其次是 Accept 校验 peer 失败时顺手把条目删掉，原主反而取不到；还有 Revoke 只减计数、条目仍留在表里，之后 Accept 又能命中。

硬约束：并发上，未决表用互斥锁保护，同一 nonce 的校验与摘除必须是一步，撤销与接受相撞时只能有一方生效且都不得重复交出 fd；锁的粒度要覆盖校验、摘除与计数更新，持锁期间不拷贝大块请求体。恢复上，快照与表操作互斥，恢复后未决计数、摘要与到期时间与快照一致，重复恢复同一份快照结果相同。

验收：go test ./... 15 条全过，不得改测试或写死输入；除跑通用例，还要自己补并发与重启场景：多个 goroutine 抢同一 nonce、撤销与接受同时发生、快照后在新 guard 上继续交接；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-02`
- 实现文件：`helper_handoff_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`HelperHandoffGuard`
