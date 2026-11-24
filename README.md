# 资源指示器受众收窄与资源退役治理

## 题面

资源指示器上线后，安全评审提两个问题：调用方申请未登记资源，服务端丢掉受众照样发令牌；资源下线后受众没从在用令牌移除，旧令牌还能访问退役接口。落点 gsb/batch48-go-safety-a-15 的 resource_audience_guard.go，类型 ResourceAudienceGuard。签名保持：NewResourceAudienceGuard(AudienceLimits) *ResourceAudienceGuard、RegisterResource(tenantID, resourceID, uri string, scopes []string) error、Narrow(clientID string, requested []string) ([]string, error)、Issue(clientID, subject string, audiences, scopes []string) (Token, error)、VerifyAudience(rawToken, resourceURI string) (Verdict, error)、RetireResource(resourceID string) (int, error)。

要修：空租户、空资源号、空客户端号分别报 ErrBlankTenant、ErrBlankResource、ErrBlankClient；URI 非法报 ErrInvalidURI，无 scope 报 ErrNoScope；受众只能取自已登记且属于该租户的资源；跨资源复用一律拒绝；未登记的资源指示器要整体拒绝签发，不能静默丢弃该受众后照常发令牌；同一 URI 规范化后重复登记拒绝，末尾斜杠与大小写按规范处理；令牌受众数与租户资源数设限；空主体报 ErrBlankSubject，受众为空报 ErrNoAudience；未知客户端报 ErrUnknownClient；验证要区分受众不匹配与资源已退役；资源退役要把该受众从在用令牌移除，多受众令牌只失去该资源，其余受众仍可用。

硬约束：注册、签发、退役与验证串行，读方不见半更新；资源表、客户端归属与令牌要能快照并重启恢复；受众数与资源数按配置上限封顶。

验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自验；只改本地

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-15`
- 实现文件：`resource_audience_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ResourceAudienceGuard`
