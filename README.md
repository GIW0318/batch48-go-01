# 终端会话绑定与指纹漂移治理

## 题面

投诉：一台终端被标记丢失后，同账号另一台设备的会话也跟着掉线；被撤销的终端换个网络却能重新登录。落点 gsb/batch48-go-safety-a-01 的 device_session_binder.go，类型 DeviceSessionBinder，签名保持：NewDeviceSessionBinder(Limits) *DeviceSessionBinder、BeginSession(userID, deviceID string, fp Fingerprint) (Session, error)、ReportFingerprint(sessionID string, fp Fingerprint) (Verdict, error)、SuspendDevice(userID, deviceID, reason string) error、RevokeDevice(userID, deviceID string) error、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：设备或单设备会话数超限仍放行，报 ErrDeviceLimit、ErrSessionLimit；空账号或空设备号报 ErrBlankIdentity；指纹全等须 allow、reason_stable、满分；漂移分量按名升序列出，权重 tls_hash 30、user_agent 20、canvas 20、时区与屏幕各 10、语言与插件各 5；分量因隐私模式缺失要降权，不得直接踢线；可疑档未越踢出阈值只记事件并降权，不下发踢出；撤销后同账号其它设备会话仍有效，被撤销设备的重连必须拒绝；挂起不填原因报 ErrBadReason 且状态不变，其会话全关；撤销不存在的设备报 ErrDeviceNotFound，重复撤销幂等成功；单用户事件按 MaxEvents 只留最新若干条；恢复后新建会话序号大于此前任一会话。

硬约束：并发建会话串行裁决，设备上限只放行配置条数；判定与快照互斥，快照只见全量旧状态或全量新状态；损坏快照 Restore 报 ErrBadSnapshot 不动现网；绑定、撤销水位与失败计数跨重启恢复。

验收：go test ./... 15 条全过，测试不得修改、跳过、按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-01`
- 实现文件：`device_session_binder.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`DeviceSessionBinder`
