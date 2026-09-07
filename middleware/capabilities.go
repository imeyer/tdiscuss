package middleware

import (
	"fmt"
	"strings"

	"tailscale.com/tailcfg"
)

// BoardCapability is the peer capability tdiscuss reads out of the tailnet
// policy file.
//
// Grant it in the "app" section of a grant to give members a board role
// without touching the database:
//
//	"grants": [
//		{
//			"src": ["group:eng"],
//			"dst": ["tag:tdiscuss"],
//			"app": {
//				"github.com/imeyer/tdiscuss/cap/board": [{"role": "admin"}]
//			}
//		}
//	]
//
// The name is a constant rather than a flag on purpose: it has to match the
// policy file exactly, and a mismatch would silently grant nothing.
const BoardCapability tailcfg.PeerCapability = "github.com/imeyer/tdiscuss/cap/board"

// Board roles that BoardCapability grants recognize. Unknown roles are
// ignored, so a policy file can name a role a future version understands
// without locking out an older binary.
const roleAdmin = "admin"

// boardCapRule is a single grant entry for BoardCapability.
type boardCapRule struct {
	// Role is the board role granted to the peer. Only "admin" is recognized
	// today. Comparison is case-insensitive and surrounding space is ignored.
	Role string `json:"role"`
}

// peerGrantsAdmin reports whether the tailnet policy file grants the peer the
// admin role.
//
// A malformed grant is reported as an error and must be treated as granting
// nothing: a typo in the policy file should cost an admin their privileges,
// not lock the whole board out.
func peerGrantsAdmin(peer *Peer) (bool, error) {
	if peer == nil || len(peer.CapMap) == 0 {
		return false, nil
	}

	rules, err := tailcfg.UnmarshalCapJSON[boardCapRule](peer.CapMap, BoardCapability)
	if err != nil {
		return false, fmt.Errorf("parsing %s grant: %w", BoardCapability, err)
	}

	for _, rule := range rules {
		if strings.EqualFold(strings.TrimSpace(rule.Role), roleAdmin) {
			return true, nil
		}
	}

	return false, nil
}
