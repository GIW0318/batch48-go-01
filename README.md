# 受保护硬链接、粘滞位与交换式改名的落地判定

## 题面

巡检发现敏感文件被硬链到普通目录后仍能读，粘滞位目录里的删除与改名没拦住，交换式改名还把目录改成了环。这类判定跑在文件系统操作之前，只拿得到调用方给的元数据，不能真去碰盘，所以规则必须确定、可重放。策略由安全团队统一维护，受保护路径随业务上下线整份替换。要修 gsb/batch48-go-safety-b-10 的 hardlink_seal_guard.go，类型 HardlinkSealGuard，签名：NewHardlinkSealGuard(Options) *HardlinkSealGuard、CheckLink(src Info, dstDir Info, owner uint32) (Verdict, error)、CheckUnlink(target Info, dir Info, owner uint32) (Verdict, error)、CheckRename(src, dst Info, exchange bool) (Verdict, error)、SetProtected([]string) error、Restore([]byte) error。

边界要求：
① 任一 Info 的路径为空报 ErrBadInfo，链接、删除、改名三个入口都一样，不能只校验源。
② 源是符号链接拒绝硬链，原因码 link_symlink_source，跟随链接再判断会绕过受保护集合。
③ 源属主与有效 uid 不符拒绝，原因码 cross_owner_source，只有 AllowCrossOwner 打开才放行。
④ 受保护路径作为链接源或删除目标拒绝，原因码 protected_path，不能只看目标目录是否受保护。
⑤ 粘滞位目录下只允许文件属主、目录属主或特权方通过，否则 sticky_denied，判定用有效 uid 而不是句柄凭据。
⑥ 交换改名两侧类型不一致报 exchange_type_mismatch；两侧互指目录成环时整体拒绝，不能先解链再建链。
⑦ 允许时 Code 为 ok，拒绝时 Code 为对应原因码，同输入重复判定结果完全相同，不依赖调用顺序。
⑧ Explain 输出必须包含原因码，允许与拒绝都不能为空，排障时能直接看到是哪条规则拦下的。
⑨ SetProtected 遇空路径报 ErrBadProtected 并保留原集合；合法输入整份替换，旧路径立即失效，不做增量合并。
⑩ 同 inode 多链接删掉一个后，受保护判定对剩余链接重新生效，不按路径缓存，也不按 inode 一刀切。
⑪ Protected 按存储顺序返回，IsProtected 与判定使用同一份集合，替换后两者同时可见。
⑫ 快照保存策略与保护集合，恢复后 IsProtected 与 Protected 一致；坏快照报 ErrBadSnapshot 且不动现状。

易漏的三点：粘滞位按有效 uid 判定，不用句柄凭据；交换改名两侧互指目录时不能先解链再建链，要整体拒绝并指出环；同 inode 多链接时删一个后受保护判定对剩余链接重生效，不按路径缓存。

硬约束：判定是纯计算，不碰文件系统，可重复调用；受保护集合整体替换发布，并发判定只能看到全量旧集或全量新集，不能看到替换到一半的集合。容量上，保护条目数受 MaxProtected 限制。恢复上，恢复同时替换策略与集合，重复恢复同一份快照结果相同。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死输入；除跑通用例，还要自己补并发与重启场景：判定与 SetProtected 交叉、交换改名成环、快照恢复后继续判定、受保护集合整份替换后旧路径立即放行；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-10`
- 实现文件：`hardlink_seal_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`HardlinkSealGuard`、`Options`、`Info`、`Verdict`
