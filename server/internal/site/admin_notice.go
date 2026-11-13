package site

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// The admin page's notice to every player online: a line the admin types, which every player logged into the game
// gets as a "System" chat line (in a match, the menus, a lobby) and, in the main menu, as the game's notification
// (AdminConfig.Notice: dgserver hands it to Blaze, blaze.Server.SendNotice). The admin's session and the site's CSRF
// check and form rate guard it like every admin action (adminAction), the box beside Send confirms it, the server
// checks the text (its characters and length) and spaces the notices (at least 10 s apart), and the audit log keeps
// the admin's name and the text.

// ErrNoticeTooSoon marks AdminConfig.Notice's refusal of a notice sent too soon after the last (429); NoticeTooSoon
// makes one with the server's words.
var ErrNoticeTooSoon = errors.New("too soon after the last notice")

type noticeTooSoon struct{ msg string }

func (e noticeTooSoon) Error() string { return e.msg }
func (e noticeTooSoon) Unwrap() error { return ErrNoticeTooSoon }

// NoticeTooSoon is ErrNoticeTooSoon with msg as its text.
func NoticeTooSoon(msg string) error { return noticeTooSoon{msg} }

// noticeFormMax is the longest text the form takes before the server's own check (which allows far fewer
// characters): a longer one is refused without being read further.
const noticeFormMax = 2000

// noticeView is the notice page's data.
type noticeView struct {
	Text    string // the text to show in the box again (after a refusal)
	MaxLen  int    // the box's maxlength
	Players int    // players logged in now (-1 = not known)
}

func (s *Site) adminNotice(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.showNotice(w, r, v, http.StatusOK, "")
}

func (s *Site) showNotice(w http.ResponseWriter, r *http.Request, v *view, status int, text string) {
	v.Title = "Message to all players"
	v.Admin.Notice = &noticeView{Text: text, MaxLen: s.admin.cfg.NoticeMax, Players: -1}
	if s.cfg.Status != nil {
		v.Admin.Notice.Players = s.cfg.Status().PlayersOnline
	}
	if v.Admin.Notice.MaxLen <= 0 {
		v.Admin.Notice.MaxLen = noticeFormMax
	}
	s.adminRender(w, r, status, "admin_notice", v)
}

func (s *Site) adminSendNotice(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	raw := r.PostFormValue("text")
	show := func(status int) { s.showNotice(w, r, v, status, raw) }
	if s.admin.cfg.Notice == nil {
		s.refuseAction(w, r, sess, v, show, http.StatusConflict, "This server can't send messages to the players.")
		return
	}
	if len(raw) > noticeFormMax*4 || utf8.RuneCountInString(raw) > noticeFormMax {
		raw = ""
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Not sent: the text is far too long.")
		return
	}
	if strings.TrimSpace(raw) == "" {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Write the message first.")
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Tick the box beside Send to confirm.")
		return
	}
	who := actor(sess, r)
	sent, text, err := s.admin.cfg.Notice(raw, who.String())
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrNoticeTooSoon) {
			status = http.StatusTooManyRequests
		}
		s.refuseAction(w, r, sess, v, show, status, "Not sent: "+err.Error()+".")
		return
	}
	s.audit(w, r, who, "player notice", "", fmt.Sprintf("%q to %d logged-in connection(s)", text, sent))
	v.Done = fmt.Sprintf("Sent to %d logged-in connection(s): %q. Players in a match read it in the match's chat and "+
		"get the notification when back in the menus; players in the menus see both now.", sent, text)
	raw = ""
	show(http.StatusOK)
}
