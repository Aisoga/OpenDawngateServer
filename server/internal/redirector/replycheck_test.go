package redirector

import (
	"encoding/hex"
	"testing"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf/tdfcheck"
)

// getServerInstance's reply against Blaze::Redirector::ServerInstanceInfo's member table in the
// client, including the ADDR union (member 0, Blaze::Redirector::IpAddress).
func TestGetServerInstanceReplyMatchesClientMemberTable(t *testing.T) {
	body, err := GetServerInstanceReply("127.0.0.1", 10041)
	if err != nil {
		t.Fatal(err)
	}
	tdfcheck.Require(t, "Blaze::Redirector::ServerInstanceInfo", body)
}

// A5: with dgserver --public-host the reply names that address, HOST and IP alike
// (tdf_golden.py getserverinstance_reply_lan).
func TestGetServerInstanceReplyPublicHost(t *testing.T) {
	body, err := GetServerInstanceReply("192.168.1.20", 10041)
	if err != nil {
		t.Fatal(err)
	}
	if want := golden.Load(t).WriterCase(t, "getserverinstance_reply_lan"); hex.EncodeToString(body) != want.Hex {
		t.Errorf("got %x\nwant %s", body, want.Hex)
	}
	tdfcheck.Require(t, "Blaze::Redirector::ServerInstanceInfo", body)
}
