// Package task negotiates the input delay of a match: both peers propose a
// value, the larger one wins, changes only land on frame boundaries and stalls
// only raise the delay when the input itself was missing.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// StallThresholdMs is the stall duration above which a stall is attributed.
const StallThresholdMs = 100

// DefaultRecoveryWindow is the number of stall free commits required before
// the delay may step down.
const DefaultRecoveryWindow = 3

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
	ErrInvalidPeer       = errors.New("peer id is required")
	ErrInvalidProposal   = errors.New("delay proposal must not be negative")
	ErrDelayTooLarge     = errors.New("delay proposal exceeds the configured maximum")
	ErrPeerNotFound      = errors.New("peer has no live proposal")
	ErrInvalidFrame      = errors.New("frame must be greater than zero")
	ErrCommitRegressed   = errors.New("commit frame must increase")
	ErrConfirmAhead      = errors.New("confirmed frame is not committed yet")
	ErrConfirmRegressed  = errors.New("confirmed frame must not move backwards")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
)

// InputDelayNegotiator owns the proposals, the committed frame and the stall
// statistics.
type InputDelayNegotiator struct {
	mu          sync.Mutex
	maxDelay    int
	recovery    int
	proposals   map[string]int
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
		proposals: map[string]int{},
		stalls:    map[StallCause]int{},
	}
}

// Propose records the delay a peer wants for the coming match.
func (n *InputDelayNegotiator) Propose(peerID string, frames int) error {
	return errNotImplemented
}

// Proposal reports the live proposal of one peer.
func (n *InputDelayNegotiator) Proposal(peerID string) (int, bool) {
	return 0, false
}

// Agreed reports the larger proposal across all live peers.
func (n *InputDelayNegotiator) Agreed() int {
	return 0
}

// Drop forgets the proposal of a peer that left the handshake.
func (n *InputDelayNegotiator) Drop(peerID string) error {
	return errNotImplemented
}

// Commit lands the agreed delay on the given frame boundary.
func (n *InputDelayNegotiator) Commit(frame uint64) (int, error) {
	return 0, errNotImplemented
}

// Confirm records that the client confirmed all frames up to the given one.
func (n *InputDelayNegotiator) Confirm(frame uint64) error {
	return errNotImplemented
}

// Current reports the delay currently in force.
func (n *InputDelayNegotiator) Current() int {
	return 0
}

// ObserveStall attributes one frame stall to a cause.
func (n *InputDelayNegotiator) ObserveStall(frame uint64, ms int, cause StallCause) {
}

// StallCount reports how many stalls of a cause were attributed.
func (n *InputDelayNegotiator) StallCount(cause StallCause) int {
	return 0
}

// SetRecoveryWindow sets the number of stall free commits required for a
// downgrade.
func (n *InputDelayNegotiator) SetRecoveryWindow(w int) {
}

// RecoveryWindow reports the recovery window.
func (n *InputDelayNegotiator) RecoveryWindow() int {
	return 0
}

// MaxDelay reports the configured delay cap.
func (n *InputDelayNegotiator) MaxDelay() int {
	return 0
}

// Peers lists the peers with a live proposal in ascending id order.
func (n *InputDelayNegotiator) Peers() []string {
	return nil
}

// Snapshot serialises the proposals, the committed frame and the stall
// statistics.
func (n *InputDelayNegotiator) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole negotiator state with a payload.
func (n *InputDelayNegotiator) Restore(data []byte) error {
	return errNotImplemented
}
