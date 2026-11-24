# Go 实现题

## 题面

投诉：密钥轮换活动被手工暂停后恢复，又从第一批重新下发，同一批对象被算了两次；有租户失败率升高后整批回滚，却把还没轮换的对象记成已轮换。落点 gsb/batch48-go-safety-a-08 的 rotation_campaign_orchestrator.go，类型 RotationCampaignOrchestrator，包名 task。

签名保持：NewRotationCampaignOrchestrator(opts OrchestratorOptions) *RotationCampaignOrchestrator、Plan(tenantID string, targets []Target, batchSize int) (Campaign, error)、Start(campaignID string) error、ApplyBatch(campaignID string) (BatchResult, error)、ReportBatch(campaignID string, batch int, failed []string) (BatchResult, error)、Pause(campaignID string) error、Resume(campaignID string) error、Rollback(campaignID string) (int, error)、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：批次按声明顺序切分，总数向上取整；同租户同时只允许一个活动；只有运行态能下发批次；下发只登记本批，落账前不计进度；同一批重复落账不重复计数并报错；未下发或越界批次不能落账；失败对象必须属于本批；单批失败率超门槛自动暂停，累计失败率超门槛只能回滚、不能继续推进；暂停期间已下发批次仍可能完成回调，恢复后按回调落账而不是整批重放；回滚只覆盖已施加批次，未开始的批次保持原状，不得把未施加对象记为已轮换；快照跨重启恢复进度与已落账集合，续跑从下一批继续。

硬约束：批次推进串行，并发下发不能把同一批发两次，也不能跨过未落账的批次；对象数有硬上限，超限拒绝。验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
cd batch48-go-safety-a-08
go test ./...
```

## 模块信息

- module: gsb/batch48-go-safety-a-08
- 源文件: rotation_campaign_orchestrator.go
- 包名: task
- 测试目录: tests/
