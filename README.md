# 互斥权限对冲突检测、授予阻断与临时豁免到期

## 题面

关账审计发现一人既审批又付款，系统全程放行；互斥权限挂角色也不报冲突。落点 gsb/batch48-go-safety-a-05 sod_conflict_guard.go（SodConflictGuard），签名：AddConflict(tenantID, permA, permB, mode string) error、Check(tenantID, subjectID string, add PermissionSet) (Verdict, error)、GrantWithCheck(tenantID, subjectID, perm string) (Grant, error)、Exempt(tenantID, subjectID, pairID string, until time.Time, reason string) (Exemption, error)、Expire(now time.Time) int、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：空白租户/权限、两侧同权限报 ErrBlankArg、ErrSamePairPerms；未知模式报 ErrBadMode；同一对权限反向登记留一条、结论一致；互斥对超上限报 ErrPairLimit；已持一侧再授另一侧须阻断，动作 block、原因 sod_block、冲突列对 ID 不落权限；告警模式放行、动作 alert、原因 sod_alert；审计超 MaxAuditPerTenant 淘汰最旧、序号递增；Check 预判不改权限，批量含两侧判阻断；两侧经角色继承也算冲突，不能只看直接授予；豁免缺到期/原因为空报 ErrInvalidExemption、ErrBlankArg，未知对报 ErrPairNotFound，单主体超限报 ErrExemptionLimit；豁免到期移除记录、新授予恢复阻断，已持权限不回收、产待复核记录；恢复还原互斥对、授予、角色继承、豁免到期与序号，坏快照报 ErrBadSnapshot 不动现网。

硬约束：判定与落权限同临界区，并发授予同一互斥对两侧一个成功；快照只见全量旧或新；四张表按配置硬顶。

验收：go test ./... 15 条全过，不得改测试、跳过、写死输入；并发与重启自验；本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-05`
- 实现文件：`sod_conflict_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SodConflictGuard`、`SodConfig`、`ConflictPair`、`Verdict`、`Grant`、`Exemption`、`AuditRecord`、`ReviewItem`
