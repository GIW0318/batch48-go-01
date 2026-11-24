package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Orchestrator failures.
var (
	ErrBadRequest     = errors.New("erase request is incomplete or malformed")
	ErrDeviceUnknown  = errors.New("device is not registered")
	ErrDeviceState    = errors.New("device state does not allow the operation")
	ErrDeviceBusy     = errors.New("device already owns an active erase task")
	ErrTaskNotFound   = errors.New("erase task does not exist")
	ErrBadTransition  = errors.New("task state does not allow the operation")
	ErrRunningLimit   = errors.New("concurrent erase quota is exhausted")
	ErrRetryLimit     = errors.New("task exhausted its retry budget")
	ErrDigestMismatch = errors.New("verification digest is missing or malformed")
	ErrReceiptExists  = errors.New("completion receipt already exists for the task")
	ErrBadSnapshot    = errors.New("snapshot payload is corrupt")
)

// DeviceState is the lifecycle state of one medium.
type DeviceState string

// The four lifecycle states of a device.
const (
	DeviceOnline         DeviceState = "online"
	DeviceRetiring       DeviceState = "retiring"
	DeviceErased         DeviceState = "erased"
	DeviceDecommissioned DeviceState = "decommissioned"
)

// Scope is the part of the medium an erase task covers.
type Scope string

// The two erase scopes.
const (
	ScopeWholeDisk Scope = "whole_disk"
	ScopePartition Scope = "partition"
)

// TaskState is the lifecycle state of one erase task.
type TaskState string

// The task states, including the cancellation window of a running task.
const (
	TaskPending    TaskState = "pending"
	TaskRunning    TaskState = "running"
	TaskCancelling TaskState = "cancelling"
	TaskCancelled  TaskState = "cancelled"
	TaskVerified   TaskState = "verified"
	TaskCompleted  TaskState = "completed"
)

// Limits bounds one orchestrator.
type Limits struct {
	MaxRunning int
	MaxRetries int
}

// Request is one erase submission.
type Request struct {
	DeviceID string
	Scope    Scope
	Method   string
	Target   string
}

// Task is one erase task.
type Task struct {
	ID       int
	DeviceID string
	Scope    Scope
	Method   string
	Target   string
	State    TaskState
	Attempts int
}

// Receipt is the non-duplicable proof of one completed erase.
type Receipt struct {
	Seq      int
	TaskID   int
	DeviceID string
	Method   string
	Digest   string
	Covered  string
}

// DeviceInfo is the operator view of one device.
type DeviceInfo struct {
	ID     string
	State  DeviceState
	TaskID int
}

// Stats counts orchestrator activity.
type Stats struct {
	Submitted int
	Started   int
	Cancelled int
	Completed int
}

// EraseOrchestrator schedules secure erase tasks across devices.
type EraseOrchestrator struct {
	limits      Limits
	devices     map[string]DeviceState
	tasks       map[int]Task
	order       []int
	running     int
	nextTaskID  int
	nextReceipt int
	receipts    []Receipt
	stats       Stats
}

// NewEraseOrchestrator builds an orchestrator with the given limits.
func NewEraseOrchestrator(limits Limits) *EraseOrchestrator {
	return &EraseOrchestrator{limits: limits}
}

// AddDevice registers a device in the online state.
func (o *EraseOrchestrator) AddDevice(deviceID string) error {
	return errNotImplemented
}

// Submit queues one erase request for a device.
func (o *EraseOrchestrator) Submit(req Request) (Task, error) {
	return Task{}, errNotImplemented
}

// Start moves a pending task into the running set when quota allows.
func (o *EraseOrchestrator) Start(taskID int) (Task, error) {
	return Task{}, errNotImplemented
}

// Cancel stops a pending task or opens the cancellation window of a running one.
func (o *EraseOrchestrator) Cancel(taskID int) (Task, error) {
	return Task{}, errNotImplemented
}

// Fail records a failed attempt and either requeues or exhausts the retry budget.
func (o *EraseOrchestrator) Fail(taskID int) (Task, error) {
	return Task{}, errNotImplemented
}

// Verify records the verification digest of a running task.
func (o *EraseOrchestrator) Verify(taskID int, digest string) (Receipt, error) {
	return Receipt{}, errNotImplemented
}

// Complete persists the one receipt of a verified task.
func (o *EraseOrchestrator) Complete(taskID int) (Receipt, error) {
	return Receipt{}, errNotImplemented
}

// SetDeviceState moves a device through its lifecycle.
func (o *EraseOrchestrator) SetDeviceState(deviceID string, state DeviceState) error {
	return errNotImplemented
}

// Task returns one task by id.
func (o *EraseOrchestrator) Task(taskID int) (Task, error) {
	return Task{}, errNotImplemented
}

// Device returns one device by id.
func (o *EraseOrchestrator) Device(deviceID string) DeviceInfo {
	return DeviceInfo{}
}

// Queue returns the pending task ids in submission order.
func (o *EraseOrchestrator) Queue() []int {
	return nil
}

// Receipts returns the persisted receipts in sequence order.
func (o *EraseOrchestrator) Receipts() []Receipt {
	return nil
}

// Snapshot serialises devices, tasks, queue order, quota and receipts.
func (o *EraseOrchestrator) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot and keeps queue order and running quota usage.
func (o *EraseOrchestrator) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (o *EraseOrchestrator) Stats() Stats {
	return Stats{}
}
