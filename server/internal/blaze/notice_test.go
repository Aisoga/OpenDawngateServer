package blaze

import (
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf/tdfcheck"
)

// SYSCHAT: an admin's text as it is sent: blanks folded, the ends trimmed; refused: empty, too long, markup, the
// game's token mark, control characters, invalid UTF-8.
func TestNoticeText(t *testing.T) {
	for _, c := range []struct{ in, want, err string }{
		{"Server update at 20:00 UTC", "Server update at 20:00 UTC", ""},
		{"  Restart\r\nin  5\tminutes  ", "Restart in 5 minutes", ""},
		{"Grüße – Neustart um 20:00 \"bald\" (ca. 5 min)!", "Grüße – Neustart um 20:00 \"bald\" (ca. 5 min)!", ""},
		{strings.Repeat("é", NoticeMaxRunes), strings.Repeat("é", NoticeMaxRunes), ""},
		{strings.Repeat("x", NoticeMaxRunes+1), "", "201 characters; at most 200"},
		{" \r\n ", "", "the text is empty"},
		{"", "", "the text is empty"},
		{"<font color='#ff0000'>red</font>", "", "markup"},
		{"a > b", "", "markup"},
		{"Q&A tonight", "", "markup"},
		{"~name~ wins", "", "markup"},
		{"bell\x07", "", "unprintable character (U+0007)"},
		{"zero​width", "", "unprintable character (U+200B)"},
		{"bad \xff byte", "", "isn't valid UTF-8"},
	} {
		got, err := NoticeText(c.in)
		switch {
		case c.err == "" && (err != nil || got != c.want):
			t.Errorf("%q: %q, %v; want %q", c.in, got, err, c.want)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%q: %q, %v; want an error with %q", c.in, got, err, c.err)
		}
	}
}

// The notice's NotifyMessage: B14's system message, TAG 2 with the first notice id, flags 3 and 1800 s, against
// tdf_golden.py's notifymessage_notice_notification.
func TestNoticeGolden(t *testing.T) {
	got := SystemMessageNotification(5, NoticeMessage(NoticeFirstID, "Server update at 20:00 UTC"), testTime)
	tdfcheck.Require(t, "Blaze::Messaging::ServerMessage", got)
	if h, want := hex.EncodeToString(got), golden.Load(t).WriterCase(t, "notifymessage_notice_notification").Hex; h != want {
		t.Errorf("got %s\nwant %s", h, want)
	}
}

// A notice reaches every logged-in connection at once, each with the next record id (so the client shows a text it
// showed before); one within NoticeGap of the last is refused and sends nothing; a refused text sends nothing; a later
// login doesn't get it; the log names the sender.
func TestNoticePushedToOnlineClients(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	ts := newTCPServer(t, func(s *Server) { s.Now = clock.now })
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	want := func(c *tcpClient, id, text string) {
		t.Helper()
		tag, attrs := c.systemNote()
		w := map[int64]string{BroadcastAttrID: id, BroadcastAttrFlags: "3", BroadcastAttrSeconds: "1800", BroadcastAttrText: text}
		if tag != SystemTagBroadcast || !reflect.DeepEqual(attrs, w) {
			t.Errorf("%s: tag %d %v, want %v", c.name, tag, attrs, w)
		}
	}
	n, err := ts.s.SendNotice("Server update\nat 20:00 UTC", "admin boss (account 1000000001)")
	if err != nil || n != (Notice{ID: 200, Text: "Server update at 20:00 UTC", Connections: 2}) {
		t.Fatalf("SendNotice: %+v, %v", n, err)
	}
	want(a, "200", "Server update at 20:00 UTC")
	want(b, "200", "Server update at 20:00 UTC")
	// within the gap: refused, nothing sent
	clock.add(9 * time.Second)
	_, err = ts.s.SendNotice("Server update at 20:00 UTC", "admin boss (account 1000000001)")
	var soon *NoticeTooSoonError
	if !errors.Is(err, ErrNoticeTooSoon) || !errors.As(err, &soon) || soon.Wait != time.Second ||
		!strings.Contains(err.Error(), "send this one in 1 s") {
		t.Errorf("within the gap: %v", err)
	}
	// a refused text: nothing sent, and it doesn't count as the last notice
	clock.add(time.Second)
	if _, err := ts.s.SendNotice("<b>x</b>", "admin boss (account 1000000001)"); err == nil {
		t.Error("markup sent")
	}
	a.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	// the same text again after the gap: a new id, so the client shows it again
	if n, err := ts.s.SendNotice("Server update at 20:00 UTC", "admin boss (account 1000000001)"); err != nil || n.ID != 201 {
		t.Fatalf("again: %+v, %v", n, err)
	}
	want(a, "201", "Server update at 20:00 UTC")
	want(b, "201", "Server update at 20:00 UTC")
	// a later login gets no notice
	c := ts.client("charlie@example.com")
	c.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	if log := ts.log.String(); !strings.Contains(log, `notice 200 from admin boss (account 1000000001): "Server update at 20:00 UTC" `+
		`(flags 3, 1800 s); sent to 2 logged-in connections`) || !strings.Contains(log, "notice 201 from") {
		t.Errorf("log:\n%s", log)
	}
}

// The notice's id range stays clear of every other broadcast record id the server or Start-Client's front uses.
func TestNoticeIDsApart(t *testing.T) {
	for _, id := range []int64{MaxBroadcasts, OnlineCountID, MapNoteID, RulesNoteID} {
		if id >= NoticeFirstID {
			t.Errorf("id %d reaches the notices' %d", id, NoticeFirstID)
		}
	}
	if NoticeFirstID+1_000_000 >= JoinInviteFirst {
		t.Errorf("the notices' ids reach Start-Client's notes (%d)", JoinInviteFirst)
	}
}
