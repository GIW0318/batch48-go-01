package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by RotationCampaignOrchestrator.
var (
	ErrInvalidPlan          = errors.New("invalid rotation plan")
	ErrTargetLimit          = errors.New("target limit reached")
	ErrCampaignExists       = errors.New("tenant already has an active campaign")
	ErrCampaignNotFound     = errors.New("campaign not found")
	ErrInvalidTransition    = errors.New("invalid campaign state transition")
	ErrBatchNotDispatched   = errors.New("batch was not dispatched")
	ErrBatchAlreadyReported = errors.New("batch already reported")
	ErrNoMoreBatches        = errors.New("no batches left to dispatch")
	ErrBadSnapshot          = errors.New("bad snapshot")
)

// Campaign statuses.
const (
	StatusPlanned          = "planned"
	StatusRunning          = "running"
	StatusPaused           = "paused"
	StatusRollbackRequired = "rollback_required"
	StatusRolledBack       = "rolled_back"
	StatusCompleted        = "completed"
)

// Target is one key object inside a rotation campaign.
type Target struct {
	ID    string `json:"id"`
	KeyID string `json:"key_id"`
}

// Campaign is the plan of a rotation campaign.
type Campaign struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	BatchSize  int    `json:"batch_size"`
	Total      int    `json:"total"`
	BatchCount int    `json:"batch_count"`
	Status     string `json:"status"`
}

// BatchResult reports one dispatched or reported batch.
type BatchResult struct {
	CampaignID string   `json:"campaign_id"`
	Batch      int      `json:"batch"`
	BatchCount int      `json:"batch_count"`
	Dispatched []string `json:"dispatched"`
	Failed     []string `json:"failed"`
	Progress   int      `json:"progress"`
	Status     string   `json:"status"`
}

// OrchestratorOptions bounds a campaign.
type OrchestratorOptions struct {
	MaxTargets           int
	FailureRateThreshold float64
}

// RotationCampaignOrchestrator runs batched key rotation campaigns.
type RotationCampaignOrchestrator struct {
	mu        sync.Mutex
	maxTarget int
	threshold float64
	seq       int
}

// NewRotationCampaignOrchestrator builds an empty orchestrator.
func NewRotationCampaignOrchestrator(opts OrchestratorOptions) *RotationCampaignOrchestrator {
	return &RotationCampaignOrchestrator{
		maxTarget: opts.MaxTargets,
		threshold: opts.FailureRateThreshold,
	}
}

// Plan registers one campaign for a tenant.
func (o *RotationCampaignOrchestrator) Plan(tenantID string, targets []Target, batchSize int) (Campaign, error) {
	return Campaign{}, errNotImplemented
}

// Start moves a planned campaign into the running state.
func (o *RotationCampaignOrchestrator) Start(campaignID string) error {
	return errNotImplemented
}

// ApplyBatch dispatches the next batch of a campaign.
func (o *RotationCampaignOrchestrator) ApplyBatch(campaignID string) (BatchResult, error) {
	return BatchResult{}, errNotImplemented
}

// ReportBatch records the callbacks of one dispatched batch.
func (o *RotationCampaignOrchestrator) ReportBatch(campaignID string, batch int, failed []string) (BatchResult, error) {
	return BatchResult{}, errNotImplemented
}

// Pause stops dispatching further batches.
func (o *RotationCampaignOrchestrator) Pause(campaignID string) error {
	return errNotImplemented
}

// Resume lets a paused campaign dispatch again.
func (o *RotationCampaignOrchestrator) Resume(campaignID string) error {
	return errNotImplemented
}

// Rollback reverts the reported batches of a campaign.
func (o *RotationCampaignOrchestrator) Rollback(campaignID string) (int, error) {
	return 0, errNotImplemented
}

// Status reports the current status of a campaign.
func (o *RotationCampaignOrchestrator) Status(campaignID string) (string, error) {
	return "", errNotImplemented
}

// Progress reports how many targets are rotated so far.
func (o *RotationCampaignOrchestrator) Progress(campaignID string) (int, error) {
	return 0, errNotImplemented
}

// Campaign returns the immutable plan of one campaign.
func (o *RotationCampaignOrchestrator) Campaign(campaignID string) (Campaign, error) {
	return Campaign{}, errNotImplemented
}

// Snapshot serialises the whole orchestrator state.
func (o *RotationCampaignOrchestrator) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the orchestrator state from a snapshot.
func (o *RotationCampaignOrchestrator) Restore(data []byte) error {
	return errNotImplemented
}
