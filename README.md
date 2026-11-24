# 沙箱路径准入、符号链接展开与租户配额

## 题面

沙箱把宿主目录按虚拟前缀挂进容器，业务代码只能看到 /data 这样的虚拟路径，真实目录由准入层换算。上线后出现几类问题：业务偶尔读到根外文件，把 /data 换成 /DATA 之后越界判断失准，符号链接互相指向时请求直接卡死，某个租户持续超额写入也没有被拦住。要修 gsb/batch48-go-safety-b-03 的 path_jail_guard.go，类型 PathJailGuard，接口为 NewPathJailGuard(Roots) (*PathJailGuard, error)、Resolve(string, Op) (Resolved, error)、SetRoots([]Root) error、ReadOnly(string) bool、Quota(string) (int, error)、Snapshot() ([]byte, error)、Restore([]byte) error。

边界要求：
① 空根集、相对根路径、根目录不存在、虚拟前缀重复或互相包含，构造时都报 ErrBadRoots，不能只跳过坏的那一条。
② 相对请求以 / 为锚折叠，清理 . 与 ..，再按最长虚拟前缀匹配，不能先做真实路径拼接。
③ 未命中任何根报 ErrNoRoot；命中落在 Exclude 子树报 ErrExcluded，排除项按根内相对路径比较。
④ 逐段展开符号链接，最多 8 跳，超限报 ErrTooManyLinks；成环时必须在跳数上限报错并带环上片段，不能死循环，也不能靠栈溢出收场。
⑤ 解析后的真实路径逃出根报 ErrEscaped，文件链接与目录链接都按同一规则处理，不允许只检查最后一段。
⑥ 读不存在的目标报 ErrNotExist；写不存在的目标回退到最后存在的父目录，返回 Exists=false 与 Parent，Real 仍是目标最终路径。
⑦ 写只读根报 ErrReadOnly；读只读根放行，并让 Resolved.ReadOnly 为 true，读写判定不能共用一套结果。
⑧ ReadOnly 对只读根、排除子树、未映射路径都返回 true，未映射按拒绝默认处理，不能因为路径看起来合法就放行。
⑨ SetQuota、Charge 传负数报 ErrBadCharge，未知租户报 ErrBadTenant，超额报 ErrQuota 且余额不变，部分扣减也不允许。
⑩ 快照保存根集与各租户余额，恢复后 Resolve 与 Quota 与快照一致；坏快照报 ErrBadSnapshot 且不动现状。
⑪ 大小写不敏感的文件系统上 /data 与 /DATA 可能是同一目录，不能只比较字符串前缀，要按文件系统语义判断。
⑫ 硬链接把 jail 内文件链到 jail 外时读放行、写必须拒绝；链接数变化时配额不得重复计，同一 inode 只算一次。

判定顺序也是最容易写反的地方：必须先折叠虚拟路径、再匹配根、最后逐段展开真实链接；顺序颠倒会让排除项与只读标记在链接展开后失效。另一个坑是把根集合当成可变切片直接改，热替换时在途请求会看到新根的半份数据。

硬约束：并发上，SetRoots 热替换时在途 Resolve 只能看到旧集或新集，不能看到拼了一半的根集；快照与解析互斥。恢复上，Restore 一次性替换根集与余额，失败不留半份状态，重复恢复同一份快照结果相同。

验收：go test ./... 15 条全过，不得改测试或写死输入；除跑通用例，还要自己补并发与重启场景：Resolve 与 SetRoots 交叉、快照后在新 jail 上继续解析与扣额；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-03`
- 实现文件：`path_jail_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`PathJailGuard`
