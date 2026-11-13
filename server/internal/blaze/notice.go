package blaze

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// An admin's notice to every player online (SYSCHAT): the website's admin page sends one line, which every logged-in connection gets as a broadcast record of its own
// (sysmsg.go's TAG 2) with the display flags 2 and 1. The client (GameUI_AddBroadcastMessage 0x004B7310 marks a new
// record's flags as pending; GameUI_UpdateBroadcastMessages 0x004B5DE0 shows them) puts flag 2's "System" chat line
// into the chat in the main menu, a lobby, shaper select and the match (game states 2, 4, 5; in the match only into the
// match's chat once it is up, RULES1b), and flag 1's notification (the bottom-right toast) in the main menu (state 2)
// only: a player in a match sees the line in the match and the toast when back in the menus, a player in the menus
// both at once. A player who logs in later gets nothing (the operator's broadcast file is for standing notices). The
// player's Blaze connection is the main server's wherever the match runs (a region's host machine too), so the
// notice reaches everyone logged in.

// NoticeFirstID is the record id of the first notice; each one takes the next (sysmsg.go's operator ids are 1-16,
// OnlineCountID 100, MapNoteID 101, RulesNoteID 102; Start-Client's own notes from JoinInviteFirst). A new id per
// notice, so the client shows each, even a text sent before (it shows a record's text once).
const NoticeFirstID = 200

// NoticeFlags are a notice's display flags: the System chat line and the main menu's notification.
const NoticeFlags = BroadcastChatLine | BroadcastNotification

// NoticeSeconds is how long a client keeps a notice's record [our choice]: a player in a match gets the main menu's
// notification when back there, if within this time.
const NoticeSeconds = 1800

// NoticeMaxRunes is the longest notice [our choice]: one chat line of a few rows.
const NoticeMaxRunes = 200

// NoticeGap is the least time between two notices [our choice]: a slip of the hand doesn't flood every chat.
const NoticeGap = 10 * time.Second

// noticeRefused are the characters a notice may not hold: the chat writes its lines as HTML (ChatWindow.AppendMessage
// adds the text to a <font> tag with TextFieldEx.appendHtml, unescaped), so < > & would be read as markup; ~ starts
// the game's text tokens (~name~) in the menus' texts.
const noticeRefused = "<>&~"

// ErrNoticeTooSoon refuses a notice sent within NoticeGap of the last one (wrapped by NoticeTooSoonError).
var ErrNoticeTooSoon = errors.New("too soon after the last notice")

// NoticeTooSoonError says how long to wait before the next notice.
type NoticeTooSoonError struct{ Wait time.Duration }

func (e *NoticeTooSoonError) Error() string {
	return fmt.Sprintf("the last notice went out less than %s ago; send this one in %d s",
		NoticeGap, int(e.Wait.Round(time.Second)/time.Second))
}

func (e *NoticeTooSoonError) Unwrap() error { return ErrNoticeTooSoon }

// NoticeText checks an admin's text and returns it as it is sent: line breaks, tabs and other spaces become blanks,
// runs of blanks one, the ends trimmed. It refuses an empty text, one over NoticeMaxRunes characters, invalid UTF-8,
// control and other unprintable characters, and noticeRefused's. The error is the admin's message.
func NoticeText(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", errors.New("the text isn't valid UTF-8")
	}
	var b strings.Builder
	blank := false
	for _, r := range raw {
		switch {
		case unicode.IsSpace(r):
			blank = b.Len() > 0
			continue
		case strings.ContainsRune(noticeRefused, r):
			return "", fmt.Errorf("the game's chat reads %q as markup; leave out the characters < > & ~", r)
		case !unicode.IsPrint(r):
			return "", fmt.Errorf("the text holds an unprintable character (%U)", r)
		}
		if blank {
			b.WriteByte(' ')
			blank = false
		}
		b.WriteRune(r)
	}
	text := b.String()
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return "", errors.New("the text is empty")
	case n > NoticeMaxRunes:
		return "", fmt.Errorf("the text has %d characters; at most %d go into one chat line", n, NoticeMaxRunes)
	}
	return text, nil
}

// NoticeMessage is a notice: a broadcast (TAG 2) with its own id, NoticeFlags, kept NoticeSeconds.
func NoticeMessage(id int64, text string) ClientMessage {
	return SystemMessage(SystemTagBroadcast, []MsgAttr{
		{BroadcastAttrID, fmt.Sprint(id)},
		{BroadcastAttrFlags, fmt.Sprint(NoticeFlags)},
		{BroadcastAttrSeconds, fmt.Sprint(NoticeSeconds)},
		{BroadcastAttrText, text},
	})
}

// Notice is a notice sent: its record id, the text as sent and how many logged-in connections got it.
type Notice struct {
	ID          int64
	Text        string
	Connections int
}

// SendNotice sends every logged-in connection raw (checked by NoticeText) as a notice with the next record id, unless
// the last notice went out less than NoticeGap ago (a *NoticeTooSoonError). from names the sender for the log (the
// admin: "admin boss (account 1000000001)").
func (s *Server) SendNotice(raw, from string) (Notice, error) {
	text, err := NoticeText(raw)
	if err != nil {
		return Notice{}, err
	}
	sm := &s.sys
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := s.Now()
	if !sm.noticeAt.IsZero() {
		if since := now.Sub(sm.noticeAt); since < NoticeGap {
			return Notice{}, &NoticeTooSoonError{Wait: NoticeGap - since}
		}
	}
	sm.noticeAt = now
	n := Notice{ID: NoticeFirstID + sm.notices, Text: text}
	sm.notices++
	conns := s.onlineConns()
	n.Connections = len(conns)
	msg := NoticeMessage(n.ID, text)
	for _, c := range conns {
		s.pushSystemLocked(c, []ClientMessage{msg})
	}
	s.Log.Logf("blaze", "notice %d from %s: %q (flags %d, %d s); sent to %d logged-in connections", n.ID, from, text,
		NoticeFlags, NoticeSeconds, n.Connections)
	return n, nil
}
