package middleware

import (
	"context"
	"net/http"
	"strings"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// TailscaleClient is the subset of the Tailscale LocalAPI client that
// authentication needs.
//
// It takes the LocalAPI response type rather than a narrowed struct of our
// own on purpose. WhoIs reports three things - the node, the user who owns
// it, and the capability grants the tailnet policy file assigns to it - and
// discarding any of them means the auth code cannot tell a person from a
// tagged machine.
type TailscaleClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

// Peer is the resolved tailnet identity behind a request.
type Peer struct {
	// LoginName is the login of the user who owns the peer's node.
	//
	// This is only a usable identity when Tags is empty. WhoIs resolves the
	// owner of the node, not the caller, so for a tagged node the control
	// plane substitutes a synthetic account that is identical for every
	// tagged node in the tailnet.
	LoginName string

	// NodeName is the peer node's FQDN, without the trailing dot. It is for
	// logging and diagnostics; never authenticate on it.
	NodeName string

	// Tags are the ACL tags applied to the peer's node. Non-empty means the
	// node is tagged: the tags are its identity and it has no human owner.
	Tags []string

	// Shared reports whether the node was shared into this tailnet from
	// another one, which means LoginName comes from a foreign identity
	// provider and its owner is not a member of this tailnet.
	Shared bool

	// CapMap holds the peer capability grants from the tailnet policy file.
	// Read typed values out of it with tailcfg.UnmarshalCapJSON.
	CapMap tailcfg.PeerCapMap
}

// IsTagged reports whether the peer's node is tagged, and therefore has no
// human owner to authenticate as.
func (p *Peer) IsTagged() bool { return len(p.Tags) > 0 }

// HasCap reports whether the tailnet policy file grants cap to this peer.
func (p *Peer) HasCap(cap tailcfg.PeerCapability) bool {
	_, ok := p.CapMap[cap]
	return ok
}

// peerFromWhoIs converts a LocalAPI WhoIs response into a Peer. It reports
// what the response said; deciding whether that is an acceptable identity is
// ResolvePeer's job.
func peerFromWhoIs(who *apitype.WhoIsResponse) *Peer {
	p := &Peer{CapMap: who.CapMap}

	if who.UserProfile != nil {
		p.LoginName = who.UserProfile.LoginName
	}

	if who.Node != nil {
		p.NodeName = strings.TrimSuffix(who.Node.Name, ".")
		p.Tags = who.Node.Tags
		p.Shared = who.Node.Sharer != 0 ||
			(who.Node.Hostinfo.Valid() && who.Node.Hostinfo.ShareeNode())
	}

	return p
}

// Querier interface for database operations
type Querier interface {
	CreateOrReturnID(ctx context.Context, email string) (CreateOrReturnIDRow, error)
}

// CreateOrReturnIDRow represents a user row from the database
type CreateOrReturnIDRow struct {
	ID        int64
	IsAdmin   bool
	IsBlocked bool
}

// Logger interface for structured logging
type Logger interface {
	DebugContext(ctx context.Context, msg string, args ...any)
	InfoContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)
	With(args ...any) Logger
}

// TelemetryConfig represents telemetry configuration
// This is a simplified version that references the actual types from the main package
type TelemetryConfig struct {
	ServiceName string
	Tracer      interface{} // Will be trace.Tracer
	Meter       interface{} // Will be metric.Meter
	Metrics     TelemetryMetrics
}

// TelemetryMetrics holds telemetry metrics
type TelemetryMetrics struct {
	RequestCounter  interface{} // Will be metric.Int64Counter
	RequestDuration interface{} // Will be metric.Float64Histogram
	ErrorCounter    interface{} // Will be metric.Int64Counter
}

// DiscussService interface for the main service
type DiscussService interface {
	ListThreads(w http.ResponseWriter, r *http.Request)
	ListThreadPosts(w http.ResponseWriter, r *http.Request)
	ListMember(w http.ResponseWriter, r *http.Request)
	NewThread(w http.ResponseWriter, r *http.Request)
	CreateThread(w http.ResponseWriter, r *http.Request)
	EditMemberProfile(w http.ResponseWriter, r *http.Request)
	EditThread(w http.ResponseWriter, r *http.Request)
	EditThreadPost(w http.ResponseWriter, r *http.Request)
	CreateThreadPost(w http.ResponseWriter, r *http.Request)
	Admin(w http.ResponseWriter, r *http.Request)
	ServeStatic(w http.ResponseWriter, r *http.Request)
	HealthCheck(w http.ResponseWriter, r *http.Request)
	MetricsHandler(w http.ResponseWriter, r *http.Request)
}
