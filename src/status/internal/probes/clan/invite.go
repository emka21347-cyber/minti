package clan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Invite is one minted invite ready for copy-paste display. Fields map
// to the on-wire JSON shape of `minti-cland invite --json`, plus a
// pre-rendered JoinCommand so the panel doesn't have to know the exact
// argument layout.
type Invite struct {
	Token       string
	ClanID      string
	Address     string
	Pin         string
	ExpiresAt   time.Time
	JoinCommand string
}

// Expired returns true if the invite's TTL has passed. The panel auto-
// dismisses on expiry; the Update handler clears m.invite when this
// flips true.
func (i *Invite) Expired() bool {
	if i == nil {
		return true
	}
	return time.Now().After(i.ExpiresAt)
}

// MintInvite shells `minti-cland invite --ttl X --json` and parses the
// response. Default TTL is 5 minutes — long enough to copy the line
// into another terminal + run it, short enough that a leaked screenshot
// doesn't grant Clan access indefinitely.
//
// Returns ErrPermissionDenied if the user can't read clan.json (same
// graceful-degradation contract as the other clan probes).
func MintInvite(ctx context.Context, ttl time.Duration) (*Invite, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	out, err := runCland(ctx, "invite", "--ttl", ttl.String(), "--json")
	if err != nil {
		if isPermDenied(out, err) {
			return nil, &ErrPermissionDenied{Wrapped: err}
		}
		return nil, err
	}
	var resp struct {
		Token       string `json:"token"`
		ClanID      string `json:"clan_id"`
		CertPin     string `json:"clan_cert_pin"`
		LanAddress  string `json:"lan_address"`
		ExpiresAt   string `json:"expires_at"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("decode invite response: %w", err)
	}
	inv := &Invite{
		Token:   resp.Token,
		ClanID:  resp.ClanID,
		Address: resp.LanAddress,
		Pin:     resp.CertPin,
	}
	if t, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
		inv.ExpiresAt = t
	}
	inv.JoinCommand = fmt.Sprintf(
		"minti-cland join --token %s --address %s --pin %s",
		inv.Token, inv.Address, inv.Pin,
	)
	return inv, nil
}
