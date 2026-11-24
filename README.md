# 介质擦除任务的编排与证明台账

## 故障描述

数据中心退役流程出了两起事故：一台磁盘的擦除任务还在排队，设备先被标记下架，系统却给它补了一条已完成记录，审计时拿不出任何证明；另一台整盘擦除跑到一半时，值班员提交了分区级擦除，任务被塞进等待队列，等整盘任务结束分区视图早已变化，新任务作用在错误的扇区上。落点 gsb/batch48-go-safety-d-15 的 secure_erase_orchestrator.go，类型 EraseOrchestrator，签名保持：NewEraseOrchestrator(Limits) *EraseOrchestrator、AddDevice(string) error、Submit(Request) (Task, error)、Start(int) (Task, error)、Cancel(int) (Task, error)、Fail(int) (Task, error)、Verify(int, string) (Receipt, error)、Complete(int) (Receipt, error)、SetDeviceState(string, DeviceState) error、Task(int) (Task, error)、Device(string) DeviceInfo、Queue() []int、Receipts() []Receipt、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() Stats。

要修：设备只认登记过的编号，未登记直接拒绝；只有在线设备能接新任务，退役中、已擦除、已下架一律拒绝；同一设备同一时刻只允许一个活动任务，重复提交报设备忙，分区级任务必须带目标分区名，整盘任务带了分区名视为非法请求；启动前要再校验一次设备状态，设备在排队期间被改动则拒绝启动，任务状态只能按待执行、运行中、已校验、已完成的顺序推进，越级流转报错；并发擦除数量受额度限制，额度用尽时任务留在队列原位置；失败重试有上限，超过上限任务转为取消而不是无限重排；校验摘要必须是带算法的完整写法，缺失或格式不对拒绝；完成只能发生一次，同一任务第二次完成必须报回执已存在，回执台账按序号从 1 开始连续追加，取消的任务不得占用序号；整盘擦除期间设备的分区视图必须冻结，分区级请求要直接拒绝而不是排进等待队列，队列在快照恢复后必须保持原顺序；取消运行中的擦除要等当前批次结束并记录未覆盖区间，不能立刻把设备标记为已擦除；等待中的任务遇到设备被标记下架时转为取消并保留空证明索引，不得留下无证据的完成记录；统计记录提交、启动、取消与完成次数。

硬约束：提交、启动、取消、完成要能并发调用，同一设备的任务状态迁移必须原子；并发额度与队列顺序在快照中一并保存，恢复后额度占用与恢复前一致；重启后任务编号、回执序号与台账不得重复；单设备重试次数与全局并发额度都有固定上限，超出按拒绝处理而不是回绕。

验收：go test ./... 的 15 条用例全过，不得修改测试、跳过用例或按固定输入写死结果；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

模块路径 `gsb/batch48-go-safety-d-15`，包名 `task`，实现文件 `secure_erase_orchestrator.go`，用例位于 `tests/`。
