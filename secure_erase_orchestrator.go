package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

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

const snapshotVersion = 1

type proofIndexEntry struct {
	TaskID     int  `json:"task_id"`
	ReceiptSeq int  `json:"receipt_seq"`
	HasReceipt bool `json:"has_receipt"`
}

type snapshotData struct {
	Version      int                     `json:"version"`
	Limits       Limits                  `json:"limits"`
	Devices      map[string]DeviceState  `json:"devices"`
	Tasks        map[int]Task            `json:"tasks"`
	Order        []int                   `json:"order"`
	Queue        []int                   `json:"queue"`
	Running      int                     `json:"running"`
	NextTaskID   int                     `json:"next_task_id"`
	NextReceipt  int                     `json:"next_receipt"`
	Receipts     []Receipt               `json:"receipts"`
	ReceiptIndex map[int]int             `json:"receipt_index"`
	Digests      map[int]string          `json:"digests"`
	Uncovered    map[int][]string        `json:"uncovered"`
	ProofIndex   map[int]proofIndexEntry `json:"proof_index"`
	Stats        Stats                   `json:"stats"`
}

// EraseOrchestrator schedules secure erase tasks across devices.
type EraseOrchestrator struct {
	mu           sync.RWMutex
	limits       Limits
	devices      map[string]DeviceState
	tasks        map[int]Task
	order        []int
	running      int
	nextTaskID   int
	nextReceipt  int
	receipts     []Receipt
	receiptIndex map[int]int
	digests      map[int]string
	uncovered    map[int][]string
	proofIndex   map[int]proofIndexEntry
	stats        Stats
}

// NewEraseOrchestrator builds an orchestrator with the given limits.
func NewEraseOrchestrator(limits Limits) *EraseOrchestrator {
	if limits.MaxRunning < 0 {
		limits.MaxRunning = 0
	}
	if limits.MaxRetries < 0 {
		limits.MaxRetries = 0
	}

	return &EraseOrchestrator{
		limits:       limits,
		devices:      make(map[string]DeviceState),
		tasks:        make(map[int]Task),
		order:        make([]int, 0),
		nextTaskID:   1,
		nextReceipt:  1,
		receipts:     make([]Receipt, 0),
		receiptIndex: make(map[int]int),
		digests:      make(map[int]string),
		uncovered:    make(map[int][]string),
		proofIndex:   make(map[int]proofIndexEntry),
	}
}

func (o *EraseOrchestrator) initLocked() {
	if o.devices == nil {
		o.devices = make(map[string]DeviceState)
	}
	if o.tasks == nil {
		o.tasks = make(map[int]Task)
	}
	if o.order == nil {
		o.order = make([]int, 0)
	}
	if o.receipts == nil {
		o.receipts = make([]Receipt, 0)
	}
	if o.receiptIndex == nil {
		o.receiptIndex = make(map[int]int)
	}
	if o.digests == nil {
		o.digests = make(map[int]string)
	}
	if o.uncovered == nil {
		o.uncovered = make(map[int][]string)
	}
	if o.proofIndex == nil {
		o.proofIndex = make(map[int]proofIndexEntry)
	}
	if o.nextTaskID <= 0 {
		o.nextTaskID = 1
	}
	if o.nextReceipt <= 0 {
		o.nextReceipt = 1
	}
	if o.limits.MaxRunning < 0 {
		o.limits.MaxRunning = 0
	}
	if o.limits.MaxRetries < 0 {
		o.limits.MaxRetries = 0
	}
}

// AddDevice registers a device in the online state.
func (o *EraseOrchestrator) AddDevice(deviceID string) error {
	if strings.TrimSpace(deviceID) == "" {
		return fmt.Errorf("%w: device id is empty", ErrBadRequest)
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	if _, exists := o.devices[deviceID]; exists {
		return fmt.Errorf("%w: device %q is already registered", ErrBadRequest, deviceID)
	}
	o.devices[deviceID] = DeviceOnline
	return nil
}

// Submit queues one erase request for a device.
func (o *EraseOrchestrator) Submit(req Request) (Task, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	state, known := o.devices[req.DeviceID]
	if !known {
		return Task{}, fmt.Errorf("%w: %q", ErrDeviceUnknown, req.DeviceID)
	}
	if state != DeviceOnline {
		return Task{}, fmt.Errorf("%w: device %q is %s", ErrDeviceState, req.DeviceID, state)
	}
	if err := validateRequest(req); err != nil {
		return Task{}, err
	}
	if activeID, busy := o.activeTaskIDLocked(req.DeviceID); busy {
		return Task{}, fmt.Errorf("%w: task %d is still active", ErrDeviceBusy, activeID)
	}

	taskID := o.nextTaskID
	o.nextTaskID++
	task := Task{
		ID:       taskID,
		DeviceID: req.DeviceID,
		Scope:    req.Scope,
		Method:   req.Method,
		Target:   req.Target,
		State:    TaskPending,
	}
	o.tasks[taskID] = task
	o.order = append(o.order, taskID)
	o.proofIndex[taskID] = proofIndexEntry{TaskID: taskID}
	o.stats.Submitted++
	return task, nil
}

// Start moves a pending task into the running set when quota allows.
func (o *EraseOrchestrator) Start(taskID int) (Task, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	task, ok := o.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}

	state, deviceKnown := o.devices[task.DeviceID]
	if task.State != TaskPending {
		if deviceKnown && state == DeviceDecommissioned && task.State == TaskCancelled {
			return task, fmt.Errorf(
				"%w: device %q was decommissioned; %w: task %d is %s",
				ErrDeviceState, task.DeviceID, ErrBadTransition, taskID, task.State,
			)
		}
		return task, fmt.Errorf("%w: task %d is %s", ErrBadTransition, taskID, task.State)
	}
	if !deviceKnown {
		return task, fmt.Errorf("%w: %q", ErrDeviceUnknown, task.DeviceID)
	}
	if state != DeviceOnline {
		if state == DeviceDecommissioned {
			task = o.cancelPendingTaskLocked(taskID)
			return task, fmt.Errorf(
				"%w: device %q is %s; %w: task was cancelled",
				ErrDeviceState, task.DeviceID, state, ErrBadTransition,
			)
		}
		return task, fmt.Errorf("%w: device %q is %s", ErrDeviceState, task.DeviceID, state)
	}
	if o.running >= o.limits.MaxRunning {
		return task, fmt.Errorf("%w: %d/%d slots used", ErrRunningLimit, o.running, o.limits.MaxRunning)
	}

	task.State = TaskRunning
	o.tasks[taskID] = task
	o.running++
	o.stats.Started++
	return task, nil
}

// Cancel stops a pending task or opens the cancellation window of a running one.
func (o *EraseOrchestrator) Cancel(taskID int) (Task, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	task, ok := o.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}

	switch task.State {
	case TaskPending:
		return o.cancelPendingTaskLocked(taskID), nil
	case TaskRunning:
		task.State = TaskCancelling
		o.tasks[taskID] = task
		o.recordUncoveredLocked(task)
		return task, nil
	case TaskCancelling:
		return o.finishCancellationLocked(taskID), nil
	case TaskCancelled:
		return task, nil
	default:
		return task, fmt.Errorf("%w: task %d is %s", ErrBadTransition, taskID, task.State)
	}
}

// Fail records a failed attempt and either requeues or exhausts the retry budget.
func (o *EraseOrchestrator) Fail(taskID int) (Task, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	task, ok := o.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	if task.State != TaskRunning {
		return task, fmt.Errorf("%w: task %d is %s", ErrBadTransition, taskID, task.State)
	}

	task.Attempts++
	if task.Attempts > o.limits.MaxRetries {
		task.State = TaskCancelled
		o.tasks[taskID] = task
		o.releaseSlotLocked()
		o.stats.Cancelled++
		o.proofIndex[taskID] = proofIndexEntry{TaskID: taskID}
		o.recordUncoveredLocked(task)
		return task, fmt.Errorf("%w: task %d after %d attempts", ErrRetryLimit, taskID, task.Attempts)
	}

	task.State = TaskPending
	o.tasks[taskID] = task
	o.releaseSlotLocked()
	return task, nil
}

// Verify records the verification digest of a running task.
func (o *EraseOrchestrator) Verify(taskID int, digest string) (Receipt, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	task, ok := o.tasks[taskID]
	if !ok {
		return Receipt{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	if task.State != TaskRunning {
		return Receipt{}, fmt.Errorf("%w: task %d is %s", ErrBadTransition, taskID, task.State)
	}
	if !validDigest(digest) {
		return Receipt{}, fmt.Errorf("%w: %q", ErrDigestMismatch, digest)
	}

	o.digests[taskID] = digest
	task.State = TaskVerified
	o.tasks[taskID] = task
	return Receipt{
		TaskID:   task.ID,
		DeviceID: task.DeviceID,
		Method:   task.Method,
		Digest:   digest,
		Covered:  coveredRange(task),
	}, nil
}

// Complete persists the one receipt of a verified task.
func (o *EraseOrchestrator) Complete(taskID int) (Receipt, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	task, ok := o.tasks[taskID]
	if !ok {
		return Receipt{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	if seq, exists := o.receiptIndex[taskID]; exists {
		receipt := o.receiptBySeqLocked(seq)
		return receipt, fmt.Errorf("%w: task %d already has receipt seq %d", ErrReceiptExists, taskID, seq)
	}
	if task.State != TaskVerified {
		return Receipt{}, fmt.Errorf("%w: task %d is %s", ErrBadTransition, taskID, task.State)
	}

	digest := o.digests[taskID]
	if !validDigest(digest) {
		return Receipt{}, fmt.Errorf("%w: task %d digest is missing", ErrDigestMismatch, taskID)
	}

	seq := o.nextReceipt
	receipt := Receipt{
		Seq:      seq,
		TaskID:   taskID,
		DeviceID: task.DeviceID,
		Method:   task.Method,
		Digest:   digest,
		Covered:  coveredRange(task),
	}
	o.receipts = append(o.receipts, receipt)
	o.receiptIndex[taskID] = seq
	o.nextReceipt++

	task.State = TaskCompleted
	o.tasks[taskID] = task
	o.releaseSlotLocked()
	o.stats.Completed++
	o.proofIndex[taskID] = proofIndexEntry{
		TaskID:     taskID,
		ReceiptSeq: seq,
		HasReceipt: true,
	}
	return receipt, nil
}

// SetDeviceState moves a device through its lifecycle.
func (o *EraseOrchestrator) SetDeviceState(deviceID string, state DeviceState) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	if _, known := o.devices[deviceID]; !known {
		return fmt.Errorf("%w: %q", ErrDeviceUnknown, deviceID)
	}
	if !validDeviceState(state) {
		return fmt.Errorf("%w: %q", ErrDeviceState, state)
	}

	o.devices[deviceID] = state
	if state == DeviceDecommissioned {
		for _, taskID := range o.order {
			task := o.tasks[taskID]
			if task.DeviceID == deviceID && task.State == TaskPending {
				o.cancelPendingTaskLocked(taskID)
			}
		}
	}
	return nil
}

// Task returns one task by id.
func (o *EraseOrchestrator) Task(taskID int) (Task, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	task, ok := o.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	return task, nil
}

// Device returns one device by id.
func (o *EraseOrchestrator) Device(deviceID string) DeviceInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()

	state, ok := o.devices[deviceID]
	if !ok {
		return DeviceInfo{}
	}

	info := DeviceInfo{ID: deviceID, State: state}
	for _, taskID := range o.order {
		task := o.tasks[taskID]
		if task.DeviceID == deviceID && isActiveTaskState(task.State) {
			info.TaskID = taskID
			break
		}
	}
	return info
}

// Queue returns the pending task ids in submission order.
func (o *EraseOrchestrator) Queue() []int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.queueSnapshotLocked()
}

// Receipts returns the persisted receipts in sequence order.
func (o *EraseOrchestrator) Receipts() []Receipt {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return append(make([]Receipt, 0, len(o.receipts)), o.receipts...)
}

// Snapshot serialises devices, tasks, queue order, quota and receipts.
func (o *EraseOrchestrator) Snapshot() ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.initLocked()

	data := snapshotData{
		Version:      snapshotVersion,
		Limits:       o.limits,
		Devices:      o.devices,
		Tasks:        o.tasks,
		Order:        append([]int(nil), o.order...),
		Queue:        o.queueLocked(),
		Running:      o.running,
		NextTaskID:   o.nextTaskID,
		NextReceipt:  o.nextReceipt,
		Receipts:     append([]Receipt(nil), o.receipts...),
		ReceiptIndex: o.receiptIndex,
		Digests:      o.digests,
		Uncovered:    o.uncovered,
		ProofIndex:   o.proofIndex,
		Stats:        o.stats,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	return payload, nil
}

// Restore loads a snapshot and keeps queue order and running quota usage.
func (o *EraseOrchestrator) Restore(data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return ErrBadSnapshot
	}

	var saved snapshotData
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if err := validateSnapshot(&saved); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}

	receiptIndex := make(map[int]int, len(saved.Receipts))
	proofIndex := make(map[int]proofIndexEntry, len(saved.Tasks))
	for _, receipt := range saved.Receipts {
		receiptIndex[receipt.TaskID] = receipt.Seq
		proofIndex[receipt.TaskID] = proofIndexEntry{
			TaskID:     receipt.TaskID,
			ReceiptSeq: receipt.Seq,
			HasReceipt: true,
		}
	}
	for taskID := range saved.Tasks {
		if _, ok := proofIndex[taskID]; !ok {
			proofIndex[taskID] = proofIndexEntry{TaskID: taskID}
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.limits = saved.Limits
	o.devices = saved.Devices
	o.tasks = saved.Tasks
	o.order = append([]int(nil), saved.Order...)
	o.running = saved.Running
	o.nextTaskID = saved.NextTaskID
	o.nextReceipt = saved.NextReceipt
	o.receipts = append([]Receipt(nil), saved.Receipts...)
	o.receiptIndex = receiptIndex
	o.digests = saved.Digests
	o.uncovered = saved.Uncovered
	o.proofIndex = proofIndex
	o.stats = saved.Stats
	o.initLocked()
	return nil
}

// Stats returns the activity counters.
func (o *EraseOrchestrator) Stats() Stats {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.stats
}

func validateRequest(req Request) error {
	switch req.Scope {
	case ScopePartition:
		if strings.TrimSpace(req.Target) == "" {
			return fmt.Errorf("%w: partition target is required", ErrBadRequest)
		}
	case ScopeWholeDisk:
		if strings.TrimSpace(req.Target) != "" {
			return fmt.Errorf("%w: whole-disk task must not carry partition target %q", ErrBadRequest, req.Target)
		}
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrBadRequest, req.Scope)
	}
	return nil
}

func validDeviceState(state DeviceState) bool {
	switch state {
	case DeviceOnline, DeviceRetiring, DeviceErased, DeviceDecommissioned:
		return true
	default:
		return false
	}
}

func validTaskState(state TaskState) bool {
	switch state {
	case TaskPending, TaskRunning, TaskCancelling, TaskCancelled, TaskVerified, TaskCompleted:
		return true
	default:
		return false
	}
}

func isActiveTaskState(state TaskState) bool {
	return state == TaskPending || state == TaskRunning || state == TaskCancelling || state == TaskVerified
}

func holdsQuota(state TaskState) bool {
	return state == TaskRunning || state == TaskCancelling || state == TaskVerified
}

func validDigest(digest string) bool {
	algorithm, value, found := strings.Cut(digest, ":")
	if !found || strings.TrimSpace(algorithm) == "" || strings.TrimSpace(value) == "" {
		return false
	}
	if strings.ContainsAny(digest, " \t\r\n") || strings.Contains(value, ":") {
		return false
	}
	return true
}

func coveredRange(task Task) string {
	if task.Scope == ScopePartition {
		return task.Target
	}
	return task.DeviceID
}

func uncoveredRange(task Task) string {
	if task.Scope == ScopePartition && strings.TrimSpace(task.Target) != "" {
		return "partition:" + task.Target + ":remaining"
	}
	return "device:" + task.DeviceID + ":remaining"
}

func (o *EraseOrchestrator) activeTaskIDLocked(deviceID string) (int, bool) {
	for _, taskID := range o.order {
		task := o.tasks[taskID]
		if task.DeviceID == deviceID && isActiveTaskState(task.State) {
			return taskID, true
		}
	}
	return 0, false
}

func (o *EraseOrchestrator) queueLocked() []int {
	queue := make([]int, 0)
	for _, taskID := range o.order {
		if task, ok := o.tasks[taskID]; ok && task.State == TaskPending {
			queue = append(queue, taskID)
		}
	}
	return queue
}

func (o *EraseOrchestrator) queueSnapshotLocked() []int {
	return o.queueLocked()
}

func (o *EraseOrchestrator) cancelPendingTaskLocked(taskID int) Task {
	task := o.tasks[taskID]
	if task.State != TaskPending {
		return task
	}

	task.State = TaskCancelled
	o.tasks[taskID] = task
	o.stats.Cancelled++
	o.proofIndex[taskID] = proofIndexEntry{TaskID: taskID}
	o.recordUncoveredLocked(task)
	return task
}

func (o *EraseOrchestrator) finishCancellationLocked(taskID int) Task {
	task := o.tasks[taskID]
	if task.State != TaskCancelling {
		return task
	}

	task.State = TaskCancelled
	o.tasks[taskID] = task
	o.releaseSlotLocked()
	o.stats.Cancelled++
	o.proofIndex[taskID] = proofIndexEntry{TaskID: taskID}
	o.recordUncoveredLocked(task)
	return task
}

func (o *EraseOrchestrator) releaseSlotLocked() {
	if o.running > 0 {
		o.running--
	}
}

func (o *EraseOrchestrator) recordUncoveredLocked(task Task) {
	if _, exists := o.uncovered[task.ID]; exists {
		return
	}
	o.uncovered[task.ID] = []string{uncoveredRange(task)}
}

func (o *EraseOrchestrator) receiptBySeqLocked(seq int) Receipt {
	for _, receipt := range o.receipts {
		if receipt.Seq == seq {
			return receipt
		}
	}
	return Receipt{}
}

func validateSnapshot(s *snapshotData) error {
	if s.Version != snapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d", s.Version)
	}
	if s.NextTaskID < 1 || s.NextReceipt < 1 {
		return errors.New("sequence watermarks must be positive")
	}
	if s.Running < 0 || s.Limits.MaxRunning < 0 || s.Limits.MaxRetries < 0 {
		return errors.New("limits and quota usage must not be negative")
	}
	if s.Running > s.Limits.MaxRunning {
		return errors.New("running quota usage exceeds limit")
	}
	if s.Stats.Submitted < 0 || s.Stats.Started < 0 || s.Stats.Cancelled < 0 || s.Stats.Completed < 0 {
		return errors.New("stats must not be negative")
	}
	if s.Devices == nil || s.Tasks == nil {
		return errors.New("state maps are missing")
	}

	for deviceID, state := range s.Devices {
		if strings.TrimSpace(deviceID) == "" || !validDeviceState(state) {
			return errors.New("invalid device entry")
		}
	}

	maxTaskID := 0
	for mapID, task := range s.Tasks {
		if mapID <= 0 || task.ID != mapID || task.ID <= 0 {
			return errors.New("task id mismatch")
		}
		if task.ID > maxTaskID {
			maxTaskID = task.ID
		}
		if _, ok := s.Devices[task.DeviceID]; !ok {
			return errors.New("task references unknown device")
		}
		if !validTaskState(task.State) || task.Attempts < 0 {
			return errors.New("invalid task state or attempts")
		}
		if err := validateRequest(Request{
			DeviceID: task.DeviceID,
			Scope:    task.Scope,
			Method:   task.Method,
			Target:   task.Target,
		}); err != nil {
			return err
		}
	}
	if s.NextTaskID <= maxTaskID {
		return errors.New("task id watermark is not ahead of tasks")
	}
	if len(s.Order) != len(s.Tasks) {
		return errors.New("task order does not contain every task")
	}

	orderSeen := make(map[int]struct{}, len(s.Order))
	for i, taskID := range s.Order {
		if taskID <= 0 {
			return errors.New("invalid task id in order")
		}
		if _, seen := orderSeen[taskID]; seen {
			return errors.New("duplicate task in order")
		}
		orderSeen[taskID] = struct{}{}
		if i > 0 && taskID <= s.Order[i-1] {
			return errors.New("task order is not monotonic submission order")
		}
		if _, ok := s.Tasks[taskID]; !ok {
			return errors.New("order references missing task")
		}
	}

	expectedQueue := make([]int, 0)
	activeByDevice := make(map[string]int)
	quotaUsed := 0
	cancelledTasks := 0
	for _, taskID := range s.Order {
		task := s.Tasks[taskID]
		if task.State == TaskPending {
			expectedQueue = append(expectedQueue, taskID)
		}
		if isActiveTaskState(task.State) {
			if other, exists := activeByDevice[task.DeviceID]; exists {
				return fmt.Errorf("device %s has active tasks %d and %d", task.DeviceID, other, taskID)
			}
			activeByDevice[task.DeviceID] = taskID
		}
		if holdsQuota(task.State) {
			quotaUsed++
		}
		if task.State == TaskCancelled {
			cancelledTasks++
		}
	}
	if quotaUsed != s.Running {
		return errors.New("running quota usage does not match task states")
	}
	if len(s.Queue) != len(expectedQueue) {
		return errors.New("snapshot queue does not match pending tasks")
	}
	for i := range expectedQueue {
		if s.Queue[i] != expectedQueue[i] {
			return errors.New("snapshot queue order is inconsistent")
		}
	}

	receiptByTask := make(map[int]Receipt, len(s.Receipts))
	for i, receipt := range s.Receipts {
		if receipt.Seq != i+1 {
			return errors.New("receipt sequence is not continuous")
		}
		task, ok := s.Tasks[receipt.TaskID]
		if !ok || task.State != TaskCompleted {
			return errors.New("receipt references a non-completed task")
		}
		if receipt.DeviceID != task.DeviceID || receipt.Method != task.Method {
			return errors.New("receipt does not match its task")
		}
		if !validDigest(receipt.Digest) {
			return errors.New("receipt digest is malformed")
		}
		if _, duplicate := receiptByTask[receipt.TaskID]; duplicate {
			return errors.New("task has multiple receipts")
		}
		receiptByTask[receipt.TaskID] = receipt
	}
	if s.NextReceipt != len(s.Receipts)+1 {
		return errors.New("receipt sequence watermark is invalid")
	}
	if s.Stats.Completed != len(s.Receipts) {
		return errors.New("completed stat does not match receipt ledger")
	}
	if s.Stats.Cancelled != cancelledTasks {
		return errors.New("cancelled stat does not match task states")
	}

	for taskID, task := range s.Tasks {
		switch task.State {
		case TaskCompleted:
			receipt, ok := receiptByTask[taskID]
			if !ok {
				return errors.New("completed task lacks receipt")
			}
			if s.Digests[taskID] != receipt.Digest {
				return errors.New("completed digest does not match receipt")
			}
		case TaskVerified:
			if !validDigest(s.Digests[taskID]) {
				return errors.New("verified task lacks a valid digest")
			}
		}
	}
	for taskID, digest := range s.Digests {
		if _, ok := s.Tasks[taskID]; !ok || !validDigest(digest) {
			return errors.New("invalid stored digest")
		}
	}
	for taskID, intervals := range s.Uncovered {
		if _, ok := s.Tasks[taskID]; !ok {
			return errors.New("uncovered interval references missing task")
		}
		for _, interval := range intervals {
			if strings.TrimSpace(interval) == "" {
				return errors.New("empty uncovered interval")
			}
		}
	}
	return nil
}
