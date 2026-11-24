package task

import "errors"

// errNotImplemented marks behaviour that still has to be filled in.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by NacBindingGuard.
var (
	ErrInvalidPort        = errors.New("nac: invalid port or device identity")
	ErrUnauthorizedVlan   = errors.New("nac: vlan not authorized")
	ErrPortBound          = errors.New("nac: port already bound to another device")
	ErrIdentityConflict   = errors.New("nac: device already bound on another port")
	ErrUnknownPort        = errors.New("nac: unknown port")
	ErrUnknownDevice      = errors.New("nac: device has no binding")
	ErrIsolateFull        = errors.New("nac: isolation domain full")
	ErrBindingLimit       = errors.New("nac: binding table full")
	ErrBadBindingSnapshot = errors.New("nac: corrupt binding snapshot")
)

// DeviceIdentity is the stable identity proven by 802.1X.
type DeviceIdentity struct {
	MAC    string
	Tenant string
	CertCN string
}

// NacBinding is one authenticated device bound to a switch port.
type NacBinding struct {
	Port      string
	MAC       string
	Tenant    string
	Vlan      int
	Lease     int
	Remaining int
	State     string
	Reason    string
}

// NacOptions carries the guard limits.
type NacOptions struct {
	LeaseSeconds    int
	IsolateCapacity int
	MaxBindings     int
	AllowedVlans    []int
}

// NacAuditEvent is one ordered state transition record.
type NacAuditEvent struct {
	Seq    int
	Action string
	Port   string
	MAC    string
	Detail string
}

// NacStats summarises guard activity.
type NacStats struct {
	Authenticated int
	Rebound       int
	Released      int
	Isolated      int
	Expired       int
	Denied        int
	Active        int
	AuditEvents   int
}

// NacBindingGuard binds authenticated devices to ports and VLANs.
type NacBindingGuard struct {
	opts NacOptions
}

// NewNacBindingGuard builds a guard with the supplied limits.
func NewNacBindingGuard(opts NacOptions) *NacBindingGuard {
	return &NacBindingGuard{opts: opts}
}

// Authenticate binds a device to a port after successful 802.1X.
func (g *NacBindingGuard) Authenticate(port string, dev DeviceIdentity, vlan int) (NacBinding, error) {
	return NacBinding{}, errNotImplemented
}

// Rebind moves an already authenticated device to another port.
func (g *NacBindingGuard) Rebind(port string, dev DeviceIdentity, vlan int) (NacBinding, error) {
	return NacBinding{}, errNotImplemented
}

// Release removes the binding of a port.
func (g *NacBindingGuard) Release(port string) error {
	return errNotImplemented
}

// Isolate moves a bound port into the isolation domain.
func (g *NacBindingGuard) Isolate(port string, reason string) error {
	return errNotImplemented
}

// SetVlanWhitelist replaces the authorized VLAN list.
func (g *NacBindingGuard) SetVlanWhitelist(vlans []int) error {
	return errNotImplemented
}

// SuspendPort puts a port binding on hold after administrative shutdown.
func (g *NacBindingGuard) SuspendPort(port string) error {
	return errNotImplemented
}

// ResumePort re-enables a suspended port and reports whether re-authentication is required.
func (g *NacBindingGuard) ResumePort(port string) (bool, error) {
	return false, errNotImplemented
}

// Expire consumes the given number of seconds of every lease and drops expired bindings.
func (g *NacBindingGuard) Expire(elapsed int) []string {
	return nil
}

// BindingOf returns the binding of a port.
func (g *NacBindingGuard) BindingOf(port string) (NacBinding, bool) {
	return NacBinding{}, false
}

// IsolatedPorts returns the isolated ports in ascending order.
func (g *NacBindingGuard) IsolatedPorts() []string {
	return nil
}

// AuditTrail returns state transition records in occurrence order.
func (g *NacBindingGuard) AuditTrail() []NacAuditEvent {
	return nil
}

// Snapshot serialises the binding table, isolation set and statistics.
func (g *NacBindingGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard from a snapshot.
func (g *NacBindingGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *NacBindingGuard) Stats() NacStats {
	return NacStats{}
}
