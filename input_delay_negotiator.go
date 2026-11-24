// Package task negotiates the input delay of a match: both peers propose a
// value, the larger one wins, changes only land on frame boundaries and stalls
// only raise the delay when the input itself was missing.
package task

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// StallThresholdMs is the stall duration above which a stall is attributed.
const StallThresholdMs = 100

// DefaultRecoveryWindow is the number of stall free commits required before
// the delay may step down.
const DefaultRecoveryWindow = 3

// snapshotVersion is the on-disk layout version understood by Snapshot and
// Restore.
const snapshotVersion = 1

// StallCause names the party responsible for a frame stall.
type StallCause int

// Stall causes.
const (
	StallInput StallCause = iota
	StallRender
	StallNetwork
)

// String names the stall cause.
func (c StallCause) String() string {
	switch c {
	case StallInput:
		return "input"
	case StallRender:
		return "render"
	case StallNetwork:
		return "network"
	default:
		return "unknown"
	}
}

// Errors reported by the negotiator.
var (
	ErrInvalidPeer      = errors.New("peer id is required")
	ErrInvalidProposal  = errors.New("delay proposal must not be negative")
	ErrDelayTooLarge    = errors.New("delay proposal exceeds the configured maximum")
	ErrPeerNotFound     = errors.New("peer has no live proposal")
	ErrCommitRegressed  = errors.New("commit frame must increase")
	ErrConfirmAhead     = errors.New("confirmed frame is not committed yet")
	ErrConfirmRegressed = errors.New("confirmed frame must not move backwards")
	ErrBadSnapshot      = errors.New("snapshot payload is invalid")
)

// proposalState is one peer's live offer. value is what the peer asked for and
// joinFloor is the committed delay in force when the peer joined, so a peer
// reconnecting with a tiny offer can never drag the match below the depth that
// already committed inputs rely on.
type proposalState struct {
	Value     int `json:"value"`
	JoinFloor int `json:"join_floor"`
}

// effective is the offer that actually participates in the negotiation.
func (p proposalState) effective() int {
	if p.Value > p.JoinFloor {
		return p.Value
	}
	return p.JoinFloor
}

// InputDelayNegotiator owns the proposals, the committed frame and the stall
// statistics.
type InputDelayNegotiator struct {
	mu          sync.Mutex
	maxDelay    int
	recovery    int
	proposals   map[string]proposalState
	current     int
	committed   bool
	firstCommit uint64
	lastCommit  uint64
	confirmed   uint64
	cleanStreak int
	stalls      map[StallCause]int
}

// NewInputDelayNegotiator builds a negotiator whose delay is capped at
// maxDelay frames.
func NewInputDelayNegotiator(maxDelay int) *InputDelayNegotiator {
	if maxDelay < 0 {
		maxDelay = 0
	}
	return &InputDelayNegotiator{
		maxDelay:  maxDelay,
		recovery:  DefaultRecoveryWindow,
		proposals: map[string]proposalState{},
		stalls:    map[StallCause]int{},
	}
}

// Propose records the delay a peer wants for the coming match.
func (n *InputDelayNegotiator) Propose(peerID string, frames int) error {
	if peerID == "" {
		return ErrInvalidPeer
	}
	if frames < 0 {
		return ErrInvalidProposal
	}
	if frames > n.maxDelay {
		return ErrDelayTooLarge
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if existing, ok := n.proposals[peerID]; ok {
		// A live peer revising its offer keeps the floor it joined with.
		existing.Value = frames
		n.proposals[peerID] = existing
		return nil
	}
	// A new (possibly reconnecting) peer is pinned to at least the delay the
	// match is already running at: its offer alone can never lower the depth
	// beneath the already committed frame depth.
	floor := 0
	if n.committed {
		floor = n.current
	}
	n.proposals[peerID] = proposalState{Value: frames, JoinFloor: floor}
	return nil
}

// Proposal reports the live proposal of one peer.
func (n *InputDelayNegotiator) Proposal(peerID string) (int, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	proposal, ok := n.proposals[peerID]
	if !ok {
		return 0, false
	}
	return proposal.Value, true
}

// Agreed reports the larger proposal across all live peers.
func (n *InputDelayNegotiator) Agreed() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.agreedLocked()
}

// agreedLocked computes the larger effective offer. Callers must hold n.mu.
func (n *InputDelayNegotiator) agreedLocked() int {
	largest := 0
	for _, proposal := range n.proposals {
		if offer := proposal.effective(); offer > largest {
			largest = offer
		}
	}
	return largest
}

// Drop forgets the proposal of a peer that left the handshake.
func (n *InputDelayNegotiator) Drop(peerID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.proposals[peerID]; !ok {
		return ErrPeerNotFound
	}
	delete(n.proposals, peerID)
	return nil
}

// Commit lands the agreed delay on the given frame boundary.
func (n *InputDelayNegotiator) Commit(frame uint64) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.committed && frame <= n.lastCommit {
		return 0, ErrCommitRegressed
	}
	target := n.agreedLocked()
	if !n.committed {
		// The first boundary adopts the agreed value exactly and the
		// pre-commit proposals become running adjustments from now on.
		n.current = target
		n.committed = true
		n.firstCommit = frame
		n.lastCommit = frame
		n.cleanStreak = 0
		return n.current, nil
	}
	switch {
	case target > n.current:
		// Uplifts take effect at the very next frame boundary.
		n.current = target
		if n.current > n.maxDelay {
			n.current = n.maxDelay
		}
		n.cleanStreak = 0
	case target < n.current:
		// Downgrades release one frame only after N stall free commits.
		n.cleanStreak++
		if n.cleanStreak >= n.recovery {
			n.current--
			n.cleanStreak = 0
		}
	}
	n.lastCommit = frame
	return n.current, nil
}

// Confirm records that the client confirmed all frames up to the given one.
func (n *InputDelayNegotiator) Confirm(frame uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if frame > n.lastCommit {
		return ErrConfirmAhead
	}
	if frame < n.confirmed {
		return ErrConfirmRegressed
	}
	n.confirmed = frame
	return nil
}

// Current reports the delay currently in force.
func (n *InputDelayNegotiator) Current() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.current
}

// ObserveStall attributes one frame stall to a cause.
func (n *InputDelayNegotiator) ObserveStall(frame uint64, ms int, cause StallCause) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if frame == 0 || ms < StallThresholdMs {
		return
	}
	n.stalls[cause]++
	// Any attributed stall breaks the stall free streak, even when the cause
	// does not justify raising the input delay.
	n.cleanStreak = 0
	if cause != StallInput {
		return
	}
	// Only a missing input raises the delay, and never past the cap.
	if n.current < n.maxDelay {
		n.current++
	}
}

// StallCount reports how many stalls of a cause were attributed.
func (n *InputDelayNegotiator) StallCount(cause StallCause) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.stalls[cause]
}

// SetRecoveryWindow sets the number of stall free commits required for a
// downgrade.
func (n *InputDelayNegotiator) SetRecoveryWindow(w int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if w < 1 {
		w = 1
	}
	n.recovery = w
}

// RecoveryWindow reports the recovery window.
func (n *InputDelayNegotiator) RecoveryWindow() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.recovery
}

// MaxDelay reports the configured delay cap.
func (n *InputDelayNegotiator) MaxDelay() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.maxDelay
}

// Peers lists the peers with a live proposal in ascending id order.
func (n *InputDelayNegotiator) Peers() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.proposals) == 0 {
		return nil
	}
	peers := make([]string, 0, len(n.proposals))
	for peerID := range n.proposals {
		peers = append(peers, peerID)
	}
	sort.Strings(peers)
	return peers
}

// negotiatorSnapshot is the serialised form of the whole negotiator state.
type negotiatorSnapshot struct {
	Version     int                      `json:"version"`
	MaxDelay    int                      `json:"max_delay"`
	Recovery    int                      `json:"recovery"`
	Current     int                      `json:"current"`
	Committed   bool                     `json:"committed"`
	FirstCommit uint64                   `json:"first_commit"`
	LastCommit  uint64                   `json:"last_commit"`
	Confirmed   uint64                   `json:"confirmed"`
	CleanStreak int                      `json:"clean_streak"`
	Proposals   map[string]proposalState `json:"proposals"`
	Stalls      map[int]int              `json:"stalls"`
}

// Snapshot serialises the proposals, the committed frame and the stall
// statistics.
func (n *InputDelayNegotiator) Snapshot() ([]byte, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	payload := negotiatorSnapshot{
		Version:     snapshotVersion,
		MaxDelay:    n.maxDelay,
		Recovery:    n.recovery,
		Current:     n.current,
		Committed:   n.committed,
		FirstCommit: n.firstCommit,
		LastCommit:  n.lastCommit,
		Confirmed:   n.confirmed,
		CleanStreak: n.cleanStreak,
		Proposals:   make(map[string]proposalState, len(n.proposals)),
		Stalls:      make(map[int]int, len(n.stalls)),
	}
	for peerID, proposal := range n.proposals {
		payload.Proposals[peerID] = proposal
	}
	for cause, count := range n.stalls {
		payload.Stalls[int(cause)] = count
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrBadSnapshot
	}
	return data, nil
}

// Restore replaces the whole negotiator state with a payload.
func (n *InputDelayNegotiator) Restore(data []byte) error {
	var payload negotiatorSnapshot
	if err := json.Unmarshal(data, &payload); err != nil {
		return ErrBadSnapshot
	}
	if err := n.validateSnapshot(&payload); err != nil {
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	proposals := make(map[string]proposalState, len(payload.Proposals))
	for peerID, proposal := range payload.Proposals {
		proposals[peerID] = proposal
	}
	stalls := make(map[StallCause]int, len(payload.Stalls))
	for cause, count := range payload.Stalls {
		stalls[StallCause(cause)] = count
	}
	n.recovery = payload.Recovery
	n.current = payload.Current
	n.committed = payload.Committed
	n.firstCommit = payload.FirstCommit
	n.lastCommit = payload.LastCommit
	n.confirmed = payload.Confirmed
	n.cleanStreak = payload.CleanStreak
	n.proposals = proposals
	n.stalls = stalls
	return nil
}

// validateSnapshot checks every field without touching the live state so a
// bad payload leaves the negotiator exactly as it was.
func (n *InputDelayNegotiator) validateSnapshot(payload *negotiatorSnapshot) error {
	if payload.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if payload.MaxDelay != n.maxDelay {
		return ErrBadSnapshot
	}
	if payload.Recovery < 1 {
		return ErrBadSnapshot
	}
	if payload.Current < 0 || payload.Current > payload.MaxDelay {
		return ErrBadSnapshot
	}
	if payload.CleanStreak < 0 {
		return ErrBadSnapshot
	}
	if payload.Committed {
		if payload.LastCommit == 0 || payload.FirstCommit == 0 ||
			payload.FirstCommit > payload.LastCommit ||
			payload.Confirmed > payload.LastCommit {
			return ErrBadSnapshot
		}
	} else {
		if payload.Current != 0 || payload.CleanStreak != 0 ||
			payload.FirstCommit != 0 || payload.LastCommit != 0 ||
			payload.Confirmed != 0 {
			return ErrBadSnapshot
		}
	}
	if payload.Proposals == nil {
		return ErrBadSnapshot
	}
	largest := 0
	for peerID, proposal := range payload.Proposals {
		if peerID == "" {
			return ErrBadSnapshot
		}
		if proposal.Value < 0 || proposal.Value > payload.MaxDelay ||
			proposal.JoinFloor < 0 || proposal.JoinFloor > payload.MaxDelay {
			return ErrBadSnapshot
		}
		if !payload.Committed && proposal.JoinFloor != 0 {
			return ErrBadSnapshot
		}
		if offer := proposal.effective(); offer > largest {
			largest = offer
		}
	}
	// The restored delay must never sit below the depth of committed inputs.
	if payload.Committed && payload.Current < largest {
		return ErrBadSnapshot
	}
	for cause, count := range payload.Stalls {
		if cause < int(StallInput) || cause > int(StallNetwork) || count < 0 {
			return ErrBadSnapshot
		}
	}
	return nil
}
