package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

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

type campaignState struct {
	Campaign
	targets    []Target
	dispatched []bool
	reported   []bool
	failed     [][]string
	progress   int
}

// RotationCampaignOrchestrator runs batched key rotation campaigns.
type RotationCampaignOrchestrator struct {
	mu        sync.Mutex
	maxTarget int
	threshold float64
	seq       int
	campaigns map[string]*campaignState
}

// NewRotationCampaignOrchestrator builds an empty orchestrator.
func NewRotationCampaignOrchestrator(opts OrchestratorOptions) *RotationCampaignOrchestrator {
	return &RotationCampaignOrchestrator{
		maxTarget: opts.MaxTargets,
		threshold: opts.FailureRateThreshold,
		campaigns: make(map[string]*campaignState),
	}
}

// Plan registers one campaign for a tenant.
func (o *RotationCampaignOrchestrator) Plan(tenantID string, targets []Target, batchSize int) (Campaign, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if strings.TrimSpace(tenantID) == "" || len(targets) == 0 || batchSize <= 0 {
		return Campaign{}, ErrInvalidPlan
	}
	if o.maxTarget > 0 && len(targets) > o.maxTarget {
		return Campaign{}, ErrTargetLimit
	}

	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.ID) == "" {
			return Campaign{}, ErrInvalidPlan
		}
		if _, ok := seen[target.ID]; ok {
			return Campaign{}, ErrInvalidPlan
		}
		seen[target.ID] = struct{}{}
	}

	for _, campaign := range o.campaigns {
		if campaign.TenantID == tenantID && isActive(campaign.Status) {
			return Campaign{}, ErrCampaignExists
		}
	}

	o.seq++
	batchCount := (len(targets) + batchSize - 1) / batchSize
	campaign := &campaignState{
		Campaign: Campaign{
			ID:         fmt.Sprintf("rc-%d", o.seq),
			TenantID:   tenantID,
			BatchSize:  batchSize,
			Total:      len(targets),
			BatchCount: batchCount,
			Status:     StatusPlanned,
		},
		targets:    append([]Target(nil), targets...),
		dispatched: make([]bool, batchCount),
		reported:   make([]bool, batchCount),
		failed:     make([][]string, batchCount),
	}
	o.campaigns[campaign.ID] = campaign
	return campaign.Campaign, nil
}

// Start moves a planned campaign into the running state.
func (o *RotationCampaignOrchestrator) Start(campaignID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return err
	}
	if campaign.Status != StatusPlanned {
		return ErrInvalidTransition
	}
	campaign.Status = StatusRunning
	return nil
}

// ApplyBatch dispatches the next batch of a campaign.
func (o *RotationCampaignOrchestrator) ApplyBatch(campaignID string) (BatchResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return BatchResult{}, err
	}
	if campaign.Status != StatusRunning {
		return BatchResult{}, ErrInvalidTransition
	}

	next := -1
	for i, dispatched := range campaign.dispatched {
		if !dispatched {
			next = i
			break
		}
	}
	if next == -1 {
		return BatchResult{}, ErrNoMoreBatches
	}

	campaign.dispatched[next] = true
	return BatchResult{
		CampaignID: campaign.ID,
		Batch:      next,
		BatchCount: campaign.BatchCount,
		Dispatched: campaign.batchIDs(next),
		Progress:   campaign.progress,
		Status:     campaign.Status,
	}, nil
}

// ReportBatch records the callbacks of one dispatched batch.
func (o *RotationCampaignOrchestrator) ReportBatch(campaignID string, batch int, failed []string) (BatchResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return BatchResult{}, err
	}
	if batch < 0 || batch >= campaign.BatchCount || !campaign.dispatched[batch] {
		return BatchResult{}, ErrBatchNotDispatched
	}
	if campaign.reported[batch] {
		return BatchResult{}, ErrBatchAlreadyReported
	}
	if campaign.Status == StatusRolledBack {
		return BatchResult{}, ErrInvalidTransition
	}

	failedIDs, err := campaign.validateFailures(batch, failed)
	if err != nil {
		return BatchResult{}, err
	}

	campaign.reported[batch] = true
	campaign.failed[batch] = failedIDs
	campaign.progress += campaign.batchSize(batch) - len(failedIDs)
	campaign.updateStatusAfterReport(batch, o.threshold)

	return BatchResult{
		CampaignID: campaign.ID,
		Batch:      batch,
		BatchCount: campaign.BatchCount,
		Dispatched: campaign.batchIDs(batch),
		Failed:     append([]string(nil), failedIDs...),
		Progress:   campaign.progress,
		Status:     campaign.Status,
	}, nil
}

// Pause stops dispatching further batches.
func (o *RotationCampaignOrchestrator) Pause(campaignID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return err
	}
	if campaign.Status != StatusRunning {
		return ErrInvalidTransition
	}
	campaign.Status = StatusPaused
	return nil
}

// Resume lets a paused campaign dispatch again.
func (o *RotationCampaignOrchestrator) Resume(campaignID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return err
	}
	if campaign.Status != StatusPaused {
		return ErrInvalidTransition
	}
	campaign.Status = StatusRunning
	return nil
}

// Rollback reverts the reported batches of a campaign.
func (o *RotationCampaignOrchestrator) Rollback(campaignID string) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return 0, err
	}
	if campaign.Status == StatusRolledBack {
		return 0, ErrInvalidTransition
	}

	reverted := campaign.progress
	campaign.progress = 0
	for i := range campaign.reported {
		campaign.reported[i] = false
		campaign.failed[i] = nil
	}
	campaign.Status = StatusRolledBack
	return reverted, nil
}

// Status reports the current status of a campaign.
func (o *RotationCampaignOrchestrator) Status(campaignID string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return "", err
	}
	return campaign.Status, nil
}

// Progress reports how many targets are rotated so far.
func (o *RotationCampaignOrchestrator) Progress(campaignID string) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return 0, err
	}
	return campaign.progress, nil
}

// Campaign returns the immutable plan of one campaign.
func (o *RotationCampaignOrchestrator) Campaign(campaignID string) (Campaign, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	campaign, err := o.lookup(campaignID)
	if err != nil {
		return Campaign{}, err
	}
	return campaign.Campaign, nil
}

func (o *RotationCampaignOrchestrator) lookup(campaignID string) (*campaignState, error) {
	campaign, ok := o.campaigns[campaignID]
	if !ok {
		return nil, ErrCampaignNotFound
	}
	return campaign, nil
}

func isActive(status string) bool {
	return status != StatusRolledBack && status != StatusCompleted
}

func (c *campaignState) batchBounds(batch int) (int, int) {
	start := batch * c.BatchSize
	end := start + c.BatchSize
	if end > c.Total {
		end = c.Total
	}
	return start, end
}

func (c *campaignState) batchSize(batch int) int {
	start, end := c.batchBounds(batch)
	return end - start
}

func (c *campaignState) batchIDs(batch int) []string {
	start, end := c.batchBounds(batch)
	ids := make([]string, 0, end-start)
	for _, target := range c.targets[start:end] {
		ids = append(ids, target.ID)
	}
	return ids
}

func (c *campaignState) validateFailures(batch int, failed []string) ([]string, error) {
	start, end := c.batchBounds(batch)
	allowed := make(map[string]struct{}, end-start)
	for _, target := range c.targets[start:end] {
		allowed[target.ID] = struct{}{}
	}

	seen := make(map[string]struct{}, len(failed))
	result := make([]string, 0, len(failed))
	for _, id := range failed {
		if _, ok := allowed[id]; !ok {
			return nil, ErrInvalidPlan
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func (c *campaignState) updateStatusAfterReport(batch int, threshold float64) {
	reportedTargets := 0
	totalFailed := 0
	for i, reported := range c.reported {
		if !reported {
			continue
		}
		reportedTargets += c.batchSize(i)
		totalFailed += len(c.failed[i])
	}

	batchTargets := c.batchSize(batch)
	batchFailureRate := 0.0
	if batchTargets > 0 {
		batchFailureRate = float64(len(c.failed[batch])) / float64(batchTargets)
	}
	cumulativeFailureRate := 0.0
	if reportedTargets > 0 {
		cumulativeFailureRate = float64(totalFailed) / float64(reportedTargets)
	}

	switch {
	case cumulativeFailureRate > threshold:
		c.Status = StatusRollbackRequired
	case batchFailureRate > threshold:
		c.Status = StatusPaused
	case c.Status == StatusRollbackRequired:
		c.Status = StatusRollbackRequired
	case c.allReported():
		c.Status = StatusCompleted
	}
}

func (c *campaignState) allReported() bool {
	for _, reported := range c.reported {
		if !reported {
			return false
		}
	}
	return true
}

const snapshotVersion = 1

type snapshotCampaign struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	BatchSize  int        `json:"batch_size"`
	Total      int        `json:"total"`
	BatchCount int        `json:"batch_count"`
	Status     string     `json:"status"`
	Targets    []Target   `json:"targets"`
	Dispatched []bool     `json:"dispatched"`
	Reported   []bool     `json:"reported"`
	Failed     [][]string `json:"failed"`
	Progress   int        `json:"progress"`
}

type snapshotState struct {
	Version              int                `json:"version"`
	MaxTargets           int                `json:"max_targets"`
	FailureRateThreshold float64            `json:"failure_rate_threshold"`
	Seq                  int                `json:"seq"`
	Campaigns            []snapshotCampaign `json:"campaigns"`
}

// Snapshot serialises the whole orchestrator state.
func (o *RotationCampaignOrchestrator) Snapshot() ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	snapshot := snapshotState{
		Version:              snapshotVersion,
		MaxTargets:           o.maxTarget,
		FailureRateThreshold: o.threshold,
		Seq:                  o.seq,
		Campaigns:            make([]snapshotCampaign, 0, len(o.campaigns)),
	}

	ids := make([]string, 0, len(o.campaigns))
	for id := range o.campaigns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		campaign := o.campaigns[id]
		snapshot.Campaigns = append(snapshot.Campaigns, snapshotCampaign{
			ID:         campaign.ID,
			TenantID:   campaign.TenantID,
			BatchSize:  campaign.BatchSize,
			Total:      campaign.Total,
			BatchCount: campaign.BatchCount,
			Status:     campaign.Status,
			Targets:    append([]Target(nil), campaign.targets...),
			Dispatched: append([]bool(nil), campaign.dispatched...),
			Reported:   append([]bool(nil), campaign.reported...),
			Failed:     cloneFailures(campaign.failed),
			Progress:   campaign.progress,
		})
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, ErrBadSnapshot
	}
	return data, nil
}

// Restore rebuilds the orchestrator state from a snapshot.
func (o *RotationCampaignOrchestrator) Restore(data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(data) == 0 {
		return ErrBadSnapshot
	}
	var snapshot snapshotState
	if err := json.Unmarshal(data, &snapshot); err != nil || snapshot.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	campaigns := make(map[string]*campaignState, len(snapshot.Campaigns))
	activeTenants := make(map[string]struct{})
	for _, saved := range snapshot.Campaigns {
		if !validStatus(saved.Status) || saved.ID == "" || saved.TenantID == "" ||
			saved.BatchSize <= 0 || saved.Total <= 0 ||
			saved.BatchCount != (saved.Total+saved.BatchSize-1)/saved.BatchSize ||
			len(saved.Targets) != saved.Total ||
			len(saved.Dispatched) != saved.BatchCount ||
			len(saved.Reported) != saved.BatchCount ||
			len(saved.Failed) != saved.BatchCount ||
			(snapshot.MaxTargets > 0 && saved.Total > snapshot.MaxTargets) {
			return ErrBadSnapshot
		}
		if _, exists := campaigns[saved.ID]; exists {
			return ErrBadSnapshot
		}
		if isActive(saved.Status) {
			if _, exists := activeTenants[saved.TenantID]; exists {
				return ErrBadSnapshot
			}
			activeTenants[saved.TenantID] = struct{}{}
		}

		targetIDs := make(map[string]struct{}, len(saved.Targets))
		for _, target := range saved.Targets {
			if strings.TrimSpace(target.ID) == "" {
				return ErrBadSnapshot
			}
			if _, exists := targetIDs[target.ID]; exists {
				return ErrBadSnapshot
			}
			targetIDs[target.ID] = struct{}{}
		}

		campaign := &campaignState{
			Campaign: Campaign{
				ID:         saved.ID,
				TenantID:   saved.TenantID,
				BatchSize:  saved.BatchSize,
				Total:      saved.Total,
				BatchCount: saved.BatchCount,
				Status:     saved.Status,
			},
			targets:    append([]Target(nil), saved.Targets...),
			dispatched: append([]bool(nil), saved.Dispatched...),
			reported:   append([]bool(nil), saved.Reported...),
			failed:     cloneFailures(saved.Failed),
			progress:   saved.Progress,
		}

		if !campaign.validRestoredState() {
			return ErrBadSnapshot
		}
		campaigns[campaign.ID] = campaign
	}

	seq := snapshot.Seq
	if seq < len(campaigns) {
		seq = len(campaigns)
	}
	o.maxTarget = snapshot.MaxTargets
	o.threshold = snapshot.FailureRateThreshold
	o.seq = seq
	o.campaigns = campaigns
	return nil
}

func (c *campaignState) validRestoredState() bool {
	seenUndispatched := false
	for _, dispatched := range c.dispatched {
		if !dispatched {
			seenUndispatched = true
			continue
		}
		if seenUndispatched {
			return false
		}
	}

	expectedProgress := 0
	allReported := true
	for batch := 0; batch < c.BatchCount; batch++ {
		if c.reported[batch] && !c.dispatched[batch] {
			return false
		}
		if !c.reported[batch] {
			allReported = false
			if len(c.failed[batch]) != 0 {
				return false
			}
			continue
		}

		failedIDs, err := c.validateFailures(batch, c.failed[batch])
		if err != nil || len(failedIDs) != len(c.failed[batch]) {
			return false
		}
		expectedProgress += c.batchSize(batch) - len(failedIDs)
	}
	if expectedProgress != c.progress || c.progress < 0 || c.progress > c.Total {
		return false
	}
	if c.Status == StatusCompleted && !allReported {
		return false
	}
	return true
}

func cloneFailures(failed [][]string) [][]string {
	cloned := make([][]string, len(failed))
	for i, batch := range failed {
		cloned[i] = append([]string(nil), batch...)
	}
	return cloned
}

func validStatus(status string) bool {
	switch status {
	case StatusPlanned, StatusRunning, StatusPaused, StatusRollbackRequired, StatusRolledBack, StatusCompleted:
		return true
	default:
		return false
	}
}
