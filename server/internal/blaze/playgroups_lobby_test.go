package blaze

import (
	"testing"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// pvpCreate is a party's createPlaygroup (JOIN 0, no key: a new playgroup every time).
func pvpCreate() []byte {
	return tdf.NewWriter().Int("JOIN", 0).Struct("PGRP", func(w *tdf.Writer) {
		w.Int("MLIM", 5).String("NAME", "party")
	}).Bytes()
}

// expectNote waits for a Playgroups notification, checks it against its class and decodes it.
func (tc *tcpClient) pgNote(id uint16, class string) *tdf.Struct {
	tc.t.Helper()
	f := tc.note(ComponentPlaygroups, id)
	tdfcheck.Require(tc.t, class, f.Payload)
	return decode(tc.t, f)
}

func (tc *tcpClient) pgCall(cmd uint16, payload []byte) fire.Frame {
	tc.t.Helper()
	return tc.reply(ComponentPlaygroups, cmd, tc.send(ComponentPlaygroups, cmd, payload))
}

func wantErr(t *testing.T, what string, f fire.Frame, code uint16) {
	t.Helper()
	if f.Type != fire.ErrorReply || f.Error != code {
		t.Errorf("%s: %s error %#x, want error %#x", what, f.Type, f.Error, code)
	}
}

func wantOK(t *testing.T, what string, f fire.Frame) {
	t.Helper()
	if f.Type != fire.Reply {
		t.Errorf("%s: %s error %#x", what, f.Type, f.Error)
	}
}

// The PvP lobby's party: create, join by id, playgroup and member attributes, join controls, kick, the
// leader change when the owner leaves, destroy.
func TestPvPLobbyPlaygroup(t *testing.T) {
	ts := newTCPServer(t, nil)
	a, b, c := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com")

	f := a.pgCall(CmdPlaygroupsCreatePlaygroup, pvpCreate())
	tdfcheck.Require(t, "Blaze::Playgroups::JoinPlaygroupResponse", f.Payload)
	info, _ := decode(t, f).Sub("INFO")
	pgid, _ := info.Int("PGID")
	if owner, _ := info.Int("OWNR"); owner != a.id {
		t.Errorf("owner %d", owner)
	}
	n := a.pgNote(NotifyJoinPlaygroupID, "Blaze::Playgroups::NotifyJoinPlaygroup")
	mf, _ := n.Get("MLST")
	if perm, _ := mf.Value.(*tdf.List).Items[0].(*tdf.Struct).Int("PERM"); perm != PartyLeaderPermissions {
		t.Errorf("the party creator's PERM %#x", perm) // kick needs bit 0x04
	}

	join := tdf.NewWriter().Int("PGID", pgid).Bytes()
	wantOK(t, "join", b.pgCall(CmdPlaygroupsJoinPlaygroup, join))
	if n := b.pgNote(NotifyJoinPlaygroupID, "Blaze::Playgroups::NotifyJoinPlaygroup"); n != nil {
		if mf, _ := n.Get("MLST"); len(mf.Value.(*tdf.List).Items) != 2 {
			t.Errorf("MLST %v", mf.Value)
		}
	}
	a.pgNote(NotifyMemberJoinedPlaygroupID, "Blaze::Playgroups::NotifyMemberJoinedPlaygroup")
	wantErr(t, "join unknown", c.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", 99).Bytes()), ErrPlaygroupNotFound)

	attrs := tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV("MatchmakingRestartSyncId", "1")).Int("PGID", pgid).Bytes()
	tdfcheck.Require(t, "Blaze::Playgroups::SetPlaygroupAttributesRequest", attrs)
	wantOK(t, "set attributes", a.pgCall(CmdPlaygroupsSetPlaygroupAttributes, attrs))
	for _, x := range []*tcpClient{a, b} {
		n := x.pgNote(NotifyPlaygroupAttributesSetID, "Blaze::Playgroups::NotifyPlaygroupAttributesSet")
		if id, _ := n.Int("PGID"); id != pgid {
			t.Errorf("%s: PGID %d", x.name, id)
		}
	}
	wantErr(t, "set attributes, not a member", c.pgCall(CmdPlaygroupsSetPlaygroupAttributes, attrs), ErrPlaygroupNotInGroup)
	wantErr(t, "set attributes, not the leader", b.pgCall(CmdPlaygroupsSetPlaygroupAttributes, attrs), ErrPlaygroupNotAuthorized)

	mattrs := tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV("MatchmakingStatus", "1")).Int("EID", b.id).Int("PGID", pgid).Bytes()
	tdfcheck.Require(t, "Blaze::Playgroups::SetMemberAttributesRequest", mattrs)
	wantOK(t, "set own member attributes", b.pgCall(CmdPlaygroupsSetMemberAttributes, mattrs))
	for _, x := range []*tcpClient{a, b} {
		n := x.pgNote(NotifyMemberAttributesSetID, "Blaze::Playgroups::NotifyMemberAttributesSet")
		if eid, _ := n.Int("EID"); eid != b.id {
			t.Errorf("%s: EID %d", x.name, eid)
		}
	}
	other := tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString).Int("EID", a.id).Int("PGID", pgid).Bytes()
	wantErr(t, "member attributes of the leader by a member", b.pgCall(CmdPlaygroupsSetMemberAttributes, other), ErrPlaygroupNotAuthorized)

	closed := tdf.NewWriter().Int("OPEN", PlaygroupClosed).Int("PGID", pgid).Bytes()
	tdfcheck.Require(t, "Blaze::Playgroups::SetJoinControlsRequest", closed)
	wantErr(t, "join controls by a member", b.pgCall(CmdPlaygroupsSetPlaygroupJoinControls, closed), ErrPlaygroupNotAuthorized)
	wantOK(t, "join controls", a.pgCall(CmdPlaygroupsSetPlaygroupJoinControls, closed))
	a.pgNote(NotifyJoinControlsChangeID, "Blaze::Playgroups::NotifyJoinControlsChange")
	b.pgNote(NotifyJoinControlsChangeID, "Blaze::Playgroups::NotifyJoinControlsChange")
	wantErr(t, "join a closed party", c.pgCall(CmdPlaygroupsJoinPlaygroup, join), ErrPlaygroupClosed)

	kick := tdf.NewWriter().Int("EID", b.id).Int("PGID", pgid).Int("REAS", 0).Bytes()
	tdfcheck.Require(t, "Blaze::Playgroups::KickPlaygroupMemberRequest", kick)
	wantOK(t, "kick", a.pgCall(CmdPlaygroupsKickPlaygroupMember, kick))
	for _, x := range []*tcpClient{a, b} {
		n := x.pgNote(NotifyMemberRemovedFromPlaygroupID, "Blaze::Playgroups::NotifyMemberRemoveFromPlaygroup")
		if r, _ := n.Int("REAS"); r != PlaygroupMemberRemoveKicked {
			t.Errorf("%s: REAS %d", x.name, r)
		}
	}

	// open again; b and c join; the owner leaves: b leads
	open := tdf.NewWriter().Int("OPEN", PlaygroupOpen).Int("PGID", pgid).Bytes()
	wantOK(t, "open", a.pgCall(CmdPlaygroupsSetPlaygroupJoinControls, open))
	a.pgNote(NotifyJoinControlsChangeID, "Blaze::Playgroups::NotifyJoinControlsChange")
	wantOK(t, "rejoin", b.pgCall(CmdPlaygroupsJoinPlaygroup, join))
	b.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	a.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)
	wantOK(t, "c joins", c.pgCall(CmdPlaygroupsJoinPlaygroup, join))
	c.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	wantOK(t, "leave", a.pgCall(CmdPlaygroupsLeavePlaygroup, tdf.NewWriter().Int("PGID", pgid).Bytes()))
	for _, x := range []*tcpClient{b, c} {
		x.pgNote(NotifyMemberRemovedFromPlaygroupID, "Blaze::Playgroups::NotifyMemberRemoveFromPlaygroup")
		n := x.pgNote(NotifyLeaderChangeID, "Blaze::Playgroups::NotifyLeaderChange")
		if lid, _ := n.Int("LID"); lid != b.id {
			t.Errorf("%s: new leader %d", x.name, lid)
		}
		n = x.pgNote(NotifyMemberPermissionsChangeID, "Blaze::Playgroups::NotifyMemberPermissionsChange")
		if lid, _ := n.Int("LID"); lid != b.id {
			t.Errorf("%s: permissions for %d", x.name, lid)
		}
		if perm, _ := n.Int("PERM"); perm != PartyLeaderPermissions {
			t.Errorf("%s: PERM %#x", x.name, perm)
		}
	}

	destroy := tdf.NewWriter().Int("PGID", pgid).Bytes()
	tdfcheck.Require(t, "Blaze::Playgroups::DestroyPlaygroupRequest", destroy)
	wantErr(t, "destroy by a member", c.pgCall(CmdPlaygroupsDestroyPlaygroup, destroy), ErrPlaygroupNotAuthorized)
	wantOK(t, "destroy", b.pgCall(CmdPlaygroupsDestroyPlaygroup, destroy))
	for _, x := range []*tcpClient{b, c} {
		x.pgNote(NotifyDestroyPlaygroupID, "Blaze::Playgroups::NotifyDestroyPlaygroup")
	}
	if n := ts.s.groups.count(); n != 0 {
		t.Errorf("%d playgroups left", n)
	}
}
