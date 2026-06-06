package panels

import (
	"testing"
	"time"

	"github.com/minti/status/internal/probes/clan"
)

func TestInvite_Nil(t *testing.T) {
	// No invite minted — panel should render empty (zero-length string,
	// layout naturally skips the section).
	got := Invite(nil, refTime)
	if got != "" {
		t.Errorf("Invite(nil) = %q, want empty", got)
	}
}

func TestInvite_Active(t *testing.T) {
	inv := &clan.Invite{
		Token:     "0_hwhTZRlsqHlIDwCcCv_U4jaSxtaQE0G9qWdm-XsH4",
		ClanID:    "5725d958-2dda-401a-8eed-2e513c2ebffe",
		Address:   "192.168.56.1:7777",
		Pin:       "sha256:f6db79289845fe1204d6dc7ae6b62ca848cf9ea9be9f63c938414b1873d43967",
		ExpiresAt: refTime.Add(4*time.Minute + 31*time.Second),
		JoinCommand: "minti-cland join --token 0_hwhTZRlsqHlIDwCcCv_U4jaSxtaQE0G9qWdm-XsH4" +
			" --address 192.168.56.1:7777" +
			" --pin sha256:f6db79289845fe1204d6dc7ae6b62ca848cf9ea9be9f63c938414b1873d43967",
	}
	got := Invite(inv, refTime)
	assertGolden(t, "invite_active", got)
}

func TestInvite_Expired(t *testing.T) {
	// Past-expiry invite renders the same as nil (panel auto-dismisses
	// — Update will clear it, but a stale frame between expiry and the
	// next Update tick shouldn't show a misleading "expires in -3s").
	inv := &clan.Invite{
		Token:     "stale",
		ExpiresAt: refTime.Add(-1 * time.Second),
	}
	got := Invite(inv, refTime)
	if got != "" {
		t.Errorf("Invite(expired) = %q, want empty", got)
	}
}
