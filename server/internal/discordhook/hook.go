package discordhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults of Options.
const (
	DefaultQueueSize  = 32
	DefaultTimeout    = 10 * time.Second
	DefaultMaxBackoff = 5 * time.Minute
)

// attempts a message gets: after a 429 (Discord said when to try again) and after a network error or a 5xx answer.
const (
	rateLimitAttempts = 5
	failAttempts      = 3
)

// ErrOff is the error of a post to a hook that isn't configured (a nil *Hook).
var ErrOff = errors.New("no webhook configured")

// ErrClosed is the error of a post that was still waiting when the hook closed, or came after.
var ErrClosed = errors.New("the webhook sender has stopped")

// ErrDropped is the error of a post the full queue had no room for.
var ErrDropped = errors.New("dropped: the webhook's queue is full")

// Options tune a Hook; the zero value is the server's.
type Options struct {
	// AllowLocal accepts an http:// URL on a loopback IPv4 address (a test aid: a local fake receiver).
	AllowLocal bool
	// Client sends the requests (nil: one with 10 s timeouts and no redirects).
	Client *http.Client
	// QueueSize is the most posts and edits waiting (0: 32); more are dropped and counted.
	QueueSize int
	// MaxBackoff caps the pause after failures (0: 5 min).
	MaxBackoff time.Duration
	// Logf takes the hook's log lines (nil: none). They name the hook only by Mask.
	Logf func(format string, a ...any)
	// UserAgent is sent with each request ("" = "OpenDawngate-Server").
	UserAgent string

	sleep func(ctx context.Context, d time.Duration) error // the tests' clock (nil: a timer)
}

// Hook posts to one Discord webhook from its own goroutine, in order: Post and Edit only queue and never wait, a
// full queue drops (and counts), 429 answers and the X-RateLimit headers are obeyed, failures back off (1 s, 2 s, 4 s
// ... up to MaxBackoff). The methods of a nil *Hook do nothing (an event without a webhook).
type Hook struct {
	url, label string
	cl         *http.Client
	logf       func(string, ...any)
	ua         string
	maxBackoff time.Duration

	queue chan *job
	ctx   context.Context // canceled when Close's flush time is up: aborts the request and the waits
	stop  context.CancelFunc
	quit  chan struct{} // closed by Close: send what is queued, then end
	done  chan struct{} // the sender has ended

	mu       sync.Mutex
	closed   bool
	dropped  int // posts and edits dropped since the last log line about it
	failures int // failed requests in a row

	// sender goroutine only
	notBefore time.Time // the next request waits until then (rate limit, back-off)
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error
}

type jobKind int

const (
	jobPost jobKind = iota
	jobEdit
)

type job struct {
	kind jobKind
	ref  *Ref
	msg  Message // jobPost; an edit takes the newest message from ref
}

// Ref is a posted message: its id once Discord has answered, for later edits. Its methods are safe on a nil *Ref.
type Ref struct {
	done chan struct{} // closed when the first post has an answer (or failed)

	mu         sync.Mutex
	id         string
	err        error
	next       *Message // the newest edit not sent yet
	editQueued bool
}

func newRef() *Ref { return &Ref{done: make(chan struct{})} }

// ID is the message's id ("" until the post has succeeded, or after it failed).
func (r *Ref) ID() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.id
}

// Wait waits for the post's answer and returns the message id, or why there is none.
func (r *Ref) Wait(ctx context.Context) (string, error) {
	if r == nil {
		return "", ErrOff
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.id, r.err
}

func (r *Ref) finish(id string, err error) {
	r.mu.Lock()
	r.id, r.err = id, err
	r.mu.Unlock()
	select {
	case <-r.done:
	default:
		close(r.done)
	}
}

// New checks url (CheckURL) and starts the hook's sender.
func New(url string, o Options) (*Hook, error) {
	if err := CheckURL(url, o.AllowLocal); err != nil {
		return nil, err
	}
	h := &Hook{url: strings.TrimSpace(url), label: Mask(url), cl: o.Client, logf: o.Logf, ua: o.UserAgent,
		maxBackoff: o.MaxBackoff, quit: make(chan struct{}), done: make(chan struct{}), now: time.Now, sleep: o.sleep}
	if h.sleep == nil {
		h.sleep = sleepCtx
	}
	if h.cl == nil {
		h.cl = defaultClient()
	}
	if h.logf == nil {
		h.logf = func(string, ...any) {}
	}
	if h.ua == "" {
		h.ua = "OpenDawngate-Server"
	}
	if h.maxBackoff <= 0 {
		h.maxBackoff = DefaultMaxBackoff
	}
	n := o.QueueSize
	if n <= 0 {
		n = DefaultQueueSize
	}
	h.queue = make(chan *job, n)
	h.ctx, h.stop = context.WithCancel(context.Background())
	go h.run()
	return h, nil
}

// defaultClient times out after 10 s and follows no redirect (a webhook answers directly; a redirect would carry
// the token elsewhere).
func defaultClient() *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: DefaultTimeout}).DialContext,
		TLSHandshakeTimeout: DefaultTimeout, ResponseHeaderTimeout: DefaultTimeout}
	return &http.Client{Timeout: DefaultTimeout, Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Label is the hook's masked name for log lines (Mask).
func (h *Hook) Label() string {
	if h == nil {
		return "off"
	}
	return h.label
}

// Post queues a new message and returns its Ref at once (the id follows with Discord's answer: ?wait=true). A nil
// hook returns nil; a full queue or a closed hook returns a Ref that has already failed.
func (h *Hook) Post(m Message) *Ref {
	if h == nil {
		return nil
	}
	ref := newRef()
	if err := h.enqueue(&job{kind: jobPost, ref: ref, msg: m}); err != nil {
		ref.finish("", err)
	}
	return ref
}

// Edit replaces the message ref with m (PATCH .../messages/<id>), after the post and any earlier edits, since one
// goroutine sends them in order. Edits not sent yet are coalesced: only the newest is sent. When the post failed (no
// id) or the message is gone (deleted in Discord: 404), m is posted as a new message and ref takes its id. A nil hook
// or ref does nothing.
func (h *Hook) Edit(ref *Ref, m Message) {
	if h == nil || ref == nil {
		return
	}
	ref.mu.Lock()
	ref.next = &m
	queued := ref.editQueued
	ref.editQueued = true
	ref.mu.Unlock()
	if queued {
		return
	}
	if err := h.enqueue(&job{kind: jobEdit, ref: ref}); err != nil {
		ref.mu.Lock()
		ref.editQueued, ref.next = false, nil
		ref.mu.Unlock()
	}
}

func (h *Hook) enqueue(j *job) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrClosed
	}
	select {
	case h.queue <- j:
		return nil
	default:
		if h.dropped == 0 {
			h.logf("discord %s: the queue is full (%d waiting); dropping posts until it has room", h.label, cap(h.queue))
		}
		h.dropped++
		return ErrDropped
	}
}

// Close stops taking posts, sends what is queued for at most flush, then aborts the rest (their Refs fail with
// ErrClosed) and returns when the sender has ended. Safe to call more than once and on a nil hook.
func (h *Hook) Close(flush time.Duration) {
	if h == nil {
		return
	}
	h.mu.Lock()
	first := !h.closed
	h.closed = true
	h.mu.Unlock()
	if first {
		close(h.quit)
	}
	t := time.NewTimer(flush)
	defer t.Stop()
	select {
	case <-h.done:
	case <-t.C:
		h.stop()
		<-h.done
	}
	h.stop()
}

// run is the sender goroutine.
func (h *Hook) run() {
	defer close(h.done)
	for {
		select {
		case j := <-h.queue:
			h.do(j)
		case <-h.quit:
			for { // the rest of the queue, until it is empty or the flush time is up
				select {
				case j := <-h.queue:
					if h.ctx.Err() != nil {
						j.ref.finish("", ErrClosed)
						continue
					}
					h.do(j)
				default:
					return
				}
			}
		}
	}
}

func (h *Hook) do(j *job) {
	h.noteDrops()
	switch j.kind {
	case jobPost:
		id, err := h.send(http.MethodPost, h.url+"?wait=true", j.msg)
		if err != nil {
			h.logf("discord %s: a post failed: %v", h.label, err)
		}
		j.ref.finish(id, err)
	case jobEdit:
		j.ref.mu.Lock()
		m := j.ref.next
		j.ref.next, j.ref.editQueued = nil, false
		id := j.ref.id
		j.ref.mu.Unlock()
		if m == nil {
			return
		}
		if id != "" {
			_, err := h.send(http.MethodPatch, h.url+"/messages/"+id, *m)
			var se *statusError
			if err == nil || !errors.As(err, &se) || se.code != http.StatusNotFound {
				if err != nil {
					h.logf("discord %s: an edit failed: %v", h.label, err)
				}
				return
			}
			h.logf("discord %s: message %s is gone (deleted in Discord?); posting it anew", h.label, id)
		}
		nid, err := h.send(http.MethodPost, h.url+"?wait=true", *m)
		if err != nil {
			h.logf("discord %s: a post failed: %v", h.label, err)
			return
		}
		j.ref.finish(nid, nil)
	}
}

// noteDrops logs how many posts the full queue dropped, once it has room again.
func (h *Hook) noteDrops() {
	h.mu.Lock()
	n := h.dropped
	h.dropped = 0
	h.mu.Unlock()
	if n > 0 {
		h.logf("discord %s: %d post(s) or edit(s) dropped while the queue was full", h.label, n)
	}
}

// statusError is an HTTP answer that isn't a success.
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string {
	if e.body != "" {
		return fmt.Sprintf("Discord answered %d: %s", e.code, e.body)
	}
	return fmt.Sprintf("Discord answered %d", e.code)
}

// send sends one request, waiting for its turn (rate limit, back-off) and trying again after a 429 (up to 5 times)
// or a network error or 5xx (up to 3 times); another 4xx is final. It returns the message id of Discord's answer.
func (h *Hook) send(method, u string, m Message) (string, error) {
	body, err := Encode(m)
	if err != nil {
		return "", err
	}
	limited, failed := 0, 0
	for {
		if d := h.notBefore.Sub(h.now()); d > 0 {
			if err := h.sleep(h.ctx, d); err != nil {
				return "", ErrClosed
			}
		}
		if h.ctx.Err() != nil {
			return "", ErrClosed
		}
		id, retry, err := h.request(method, u, body)
		if err == nil {
			if h.failures > 0 {
				h.logf("discord %s: works again after %d failed request(s)", h.label, h.failures)
			}
			h.failures = 0
			return id, nil
		}
		if h.ctx.Err() != nil {
			return "", ErrClosed
		}
		var se *statusError
		switch {
		case errors.As(err, &se) && se.code == http.StatusTooManyRequests:
			limited++
			h.later(retry)
			if limited < rateLimitAttempts {
				continue
			}
		case errors.As(err, &se) && se.code < 500:
			h.backOff()
			return "", err
		default:
			failed++
			h.backOff()
			if failed < failAttempts {
				continue
			}
		}
		return "", err
	}
}

// later moves the next request to at least d from now.
func (h *Hook) later(d time.Duration) {
	if t := h.now().Add(d); t.After(h.notBefore) {
		h.notBefore = t
	}
}

// backOff counts a failure and pauses the hook: 1 s after the first, doubling up to maxBackoff.
func (h *Hook) backOff() {
	h.failures++
	d := time.Second << min(h.failures-1, 20)
	if d > h.maxBackoff || d <= 0 {
		d = h.maxBackoff
	}
	h.later(d)
}

// request does one HTTP request. It returns the answer's message id, and for a 429 how long to wait.
func (h *Hook) request(method, u string, body []byte) (id string, retry time.Duration, err error) {
	req, err := http.NewRequestWithContext(h.ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return "", 0, errors.New("the request can't be made")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", h.ua)
	resp, err := h.cl.Do(req)
	if err != nil {
		return "", 0, cleanErr(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	// the bucket: no request left until the reset
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		h.later(seconds(resp.Header.Get("X-RateLimit-Reset-After")))
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		var rl struct {
			RetryAfter float64 `json:"retry_after"`
		}
		retry = seconds(resp.Header.Get("Retry-After"))
		if json.Unmarshal(data, &rl) == nil && rl.RetryAfter > 0 {
			retry = time.Duration(rl.RetryAfter * float64(time.Second))
		}
		if retry <= 0 {
			retry = time.Second
		}
		return "", min(retry, h.maxBackoff), &statusError{code: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", 0, &statusError{code: resp.StatusCode, body: shortBody(data)}
	}
	var msg struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &msg) == nil {
		id = msg.ID
	}
	return id, 0, nil
}

// seconds reads a header's seconds ("1", "0.25"); 0 when absent or unreadable.
func seconds(v string) time.Duration {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 || f > 24*3600 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

// shortBody is Discord's error text for the log: its "message" field, else the first 200 bytes.
func shortBody(data []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &e) == nil && e.Message != "" {
		return cut(e.Message, 200)
	}
	return cut(strings.TrimSpace(string(data)), 200)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
