// Package gmshost starts and supervises the match hosts (ADR-2 decisions 3, 6 and 7; roadmap C1).
//
// A match host is one Dawngate.exe per match, with our DLL from gms/ running the GMS session and the
// server simulator. dgserver starts it with the environment DGGMS_CONTROL (this package's control
// address), DGGMS_MATCH (the match id), DGGMS_TOKEN (a random token for this match) and DGGMS_BIND
// (the address its GMS port binds, from the port pool). The host dials the control address and talks
// JSON Lines: one UTF-8 JSON object per line, its type in "t" (docs/HOST_CONTROL.md has the table).
//
//	host -> server   hello, gameReady, addPlayer, disconnectPlayer, reportGMSUpdate, playerFinish,
//	                 finishGame, playerKarma (D8c), reportGMSError, logoutGMS
//	server -> host   createGame, removePlayer, abortGame, playerRewards (B7c), updateRoster and startGame
//	                 (B15: a custom game's lobby)
//
// The lifecycle per match: spawn -> hello (token checked) -> createGame -> gameReady (the caller now
// announces the GMS address to the clients) -> ... -> finishGame -> logoutGMS -> the process exits and
// is reaped. Timeouts (no hello, no gameReady, no message for too long, no exit after logoutGMS or
// abortGame) kill the process. An exit without finishGame is an abort.
//
// The C++ side of the channel is gms/core/control (its own codec; the line formats here and there
// are pinned by the same examples in the tests of both).
//
// Remote host machines (roadmap H2, ADR-2 decision 8; agent.go, remote.go): an agent (dgserver
// --agent on a host machine) dials the same control port, proves it knows the shared agent key and
// starts hosts on that machine when the server asks. Those hosts dial the server's control port
// themselves, with their token, as local ones do. The agent messages are Go only (no C++ side):
//
//	agent -> server  agentHello, agentAuth, agentStatus, hostStarted, hostFailed, hostExited
//	server -> agent  agentChallenge, agentReady, launchHost, killHost
package gmshost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// Message types ("t").
const (
	THello            = "hello"
	TCreateGame       = "createGame"
	TGameReady        = "gameReady"
	TAddPlayer        = "addPlayer"
	TDisconnectPlayer = "disconnectPlayer"
	TReportGMSUpdate  = "reportGMSUpdate"
	TPlayerFinish     = "playerFinish"
	TFinishGame       = "finishGame"
	TReportGMSError   = "reportGMSError"
	TLogoutGMS        = "logoutGMS"
	TRemovePlayer     = "removePlayer"
	TAbortGame        = "abortGame"
	TPlayerRewards    = "playerRewards"
	TPlayerKarma      = "playerKarma"
	TUpdateRoster     = "updateRoster" // B15
	TStartGame        = "startGame"    // B15

	// agents (H2)
	TAgentHello     = "agentHello"
	TAgentChallenge = "agentChallenge"
	TAgentAuth      = "agentAuth"
	TAgentReady     = "agentReady"
	TAgentStatus    = "agentStatus"
	TLaunchHost     = "launchHost"
	THostStarted    = "hostStarted"
	THostFailed     = "hostFailed"
	TKillHost       = "killHost"
	THostExited     = "hostExited"
)

// MaxLine is the longest control line accepted (bytes, without the newline).
const MaxLine = 64 << 10

// Message is one control message; Type returns its "t".
type Message interface{ Type() string }

// Hello is the host's first line. Match and Token come from DGGMS_MATCH and DGGMS_TOKEN; PID is the
// host's process id, Build a free-form version string of the host DLL.
type Hello struct {
	Match int64  `json:"match"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
	Build string `json:"build"`
}

// RosterEntry is one player of the match.
type RosterEntry struct {
	User  int64  `json:"user"`  // Blaze id (persona id, BUID); HelloReq's user id
	Name  string `json:"name"`  // persona name
	Index int    `json:"index"` // gameplay index (HelloPlayer), 0-9
	Team  int    `json:"team"`  // game team: 1 or 2 (Blaze TIDX + 1)
	Noun  string `json:"noun"`  // the shaper's noun, "" = the host's choice (today alpha.Noun, script A)
	Bot   bool   `json:"bot"`
	// Shaper select's content for a human (D3-live step 4; docs/HOST_CONTROL.md, GMS_PROTOCOL.md *Shaper
	// select*), only in a CreateGame with ShaperSelect; each omitted when empty (a host without shaper
	// select ignores them). Nouns are asset names ("alpha.Noun"); the host takes their FNV-1.
	Shapers    []ShaperOffer       `json:"shapers,omitempty"`    // the player's list (C8 02)
	Skins      map[string][]string `json:"skins,omitempty"`      // a shaper's default skin -> its other owned skins
	SavedSkins map[string]string   `json:"savedSkins,omitempty"` // USET SKIN_0x*: default skin -> the saved skin
	Wards      []string            `json:"wards,omitempty"`      // owned ward Nouns (C8 0E kind 0)
	Announcers []string            `json:"announcers,omitempty"` // owned announcer packs (C8 0E kind 1)
	// PerkPages are the account's perk pages (D3-perks): C8 09 lists their ids, the C8 0A page becomes
	// mLoadoutID/Version, and its stats go out as the pre-game loadout C0 02 and onto the server player.
	PerkPages []PerkPage `json:"perkPages,omitempty"`
	// Party is the PvP party (the Blaze playgroup id, BTPL) the player queued with, 0 when solo (D8b): the
	// host's post-game counts a karma vote from the same party as a party vote (C1 04 +0x1C). Omitted when 0.
	Party int64 `json:"party,omitempty"`
	// Spectator marks an observer of a custom game (B15: the Blaze spectator team, TIDX 2; the custom lobby's third
	// roster): gameplay index 10-14 (GameSimulator_IsSpectatorIndex 0x008EED10), team 0. Omitted when false.
	Spectator bool `json:"spectator,omitempty"`
}

// PerkPage is one of a human's perk pages with the stats it gives (D3-perks; docs/HOST_CONTROL.md): the page's
// complete shapes' and all its gems' attributes, summed per attribute and op, and their modifiers, as the client's
// LoadoutManager_ComputePageStats 0x0088C890 adds them up.
type PerkPage struct {
	ID         uint32          `json:"id"`                   // SUMM.PID: the loadout manager's page id (page +8)
	Version    uint32          `json:"version"`              // SUMM.VERS: mLoadoutVersion
	Attributes []PerkAttribute `json:"attributes,omitempty"` // C0 02's entries, ordered by attribute, then op
	Modifiers  []string        `json:"modifiers,omitempty"`  // the passive modifiers' Lua names, sorted
}

// PerkAttribute is one summed stat of a perk page: AssetData cPerkAttributeData as C0 02 carries it.
type PerkAttribute struct {
	Attribute int   `json:"attribute"` // attribute enum 0x012464B8 (0 Power, 2 MaxHealth, ...)
	Op        int   `json:"op"`        // op enum 0x01419C70 (1 Add, 2 PercentChange, ...)
	Milli     int64 `json:"milli"`     // the value in thousandths (the client rounds a page's sums to 0.001)
}

// ShaperOffer is one entry of a player's shaper select list (C8 02's 10-byte entry, D3-live step 4).
type ShaperOffer struct {
	Noun  string `json:"noun"`            // the shaper's default skin ("alpha.Noun"), the shaper item's asset
	Flags int    `json:"flags"`           // ShaperOwned, ShaperFreeRotation, ShaperHidden, ShaperAvailable
	Offer int64  `json:"offer,omitempty"` // the store offer (FNV-1 of its PUII) shown for an unowned shaper; 0 = none
}

// The shaper list entry's flags (GMS_PROTOCOL.md *Shaper select*, the list entry's +4).
const (
	ShaperOwned        = 0x01
	ShaperFreeRotation = 0x02
	ShaperHidden       = 0x10
	ShaperAvailable    = 0x20
)

// The CreateGame.Timers keys shaper select reads (milliseconds; AssetData ShaperSelectTuning's names).
const (
	TimerShaperSelection = "ShaperSelectionTime"
	TimerRolePerkSelect  = "RolePerkSelectTime"
	TimerSkinSelect      = "SkinSelectTime"
	TimerReveal          = "RevealTime"
)

// CreateGame is the match the host loads.
type CreateGame struct {
	GameID int64         `json:"gameId"` // the Blaze game id
	Level  string        `json:"level"`  // level name, e.g. SM_Map_v5
	Mode   string        `json:"mode"`   // ModePractice, ...
	Seed   int64         `json:"seed"`
	Roster []RosterEntry `json:"roster"`
	// Timers are named durations in milliseconds for the host (e.g. shaper select); empty = the host's
	// defaults.
	Timers map[string]int64 `json:"timers,omitempty"`
	// Descriptors is the Blaze game's GameFlags attribute (nGameDescriptors; 0x50C = 1292 for the
	// practice game, 1 for a matchmade one), D8-live: the host passes it to GameSimulator_StartGame, the
	// post-game's C1 01 (which end cinematic the client plays) and the rewards. 0 = not given (omitted;
	// the host keeps its [host] match_descriptors).
	Descriptors int64 `json:"descriptors,omitempty"`
	// ShaperSelect asks the host to run shaper select (D3-live step 4, dgserver --shaper-select) with the
	// roster's content; omitted when false (the host's [host] shaper_select decides, off by default).
	ShaperSelect bool `json:"shaperSelect,omitempty"`
	// FillTeams (B10d) asks the host to fill each team with bot players up to this many seats: a matchmade game
	// that started on --mm-fill-after with open seats (matchmaker.Match.Full false) gets the matchmaker's team
	// size, unless dgserver runs with --mm-bots=false. 0 = not asked (omitted): the host's [host] bots auto rule
	// goes by the descriptors' BotsFillTeams (0x400) as before. The descriptors stay the game's GameFlags: they
	// reach the client (C1 01, the simulator's +0x19E58), and the bit can't carry a team size.
	FillTeams int `json:"fillTeams,omitempty"`
	// LevelFlags are the level flags the host loads the level with and sends in GamePrepareForStart, so every client
	// loads it with them too: the client's level loader loads a markerset whose condition names value c only when bit
	// c is set. LevelFlagHalloween shows the map's Halloween decoration (a client with zz_Halloween.snap; dgserver
	// --halloween). 0 = none (omitted): the host keeps its dgsim.ini [dgsim] flags.
	LevelFlags int64 `json:"levelFlags,omitempty"`
}

// LevelFlagHalloween is CreateGame.LevelFlags' bit for the Halloween decoration (setup/halloween.LevelFlag: the
// holiday markerset's condition hordeLevelsOnly, 0).
const LevelFlagHalloween = 1

// Modes of CreateGame: ModePractice is a game made by resetDedicatedServer (PRACTICE DAWNGATE, B8),
// ModePvP one made by the matchmaker (GameManager::startMatchmaking, B10a), ModeCustom a custom game (B15:
// resetDedicatedServer with kCreateGameFlags_Custom, PLAY > CUSTOM > CREATE GAME, or a practice group): its
// players gather in the client's custom lobby first, so the host holds every HelloReq until StartGame, and
// UpdateRoster tells it who may connect meanwhile.
const (
	ModePractice = "practice"
	ModePvP      = "pvp"
	ModeCustom   = "custom"
)

// UpdateRoster (B15, server -> host) is a custom game's lobby roster after a change (a player joined, left, was
// kicked or moved to another team): every human who may connect, with a provisional gameplay index and team. The
// host lets these users connect and holds their HelloReq until StartGame.
type UpdateRoster struct {
	Roster []RosterEntry `json:"roster"`
}

// StartGame (B15, server -> host) starts a custom game (its admin's GameManager::advanceGameState IN_GAME): the
// final roster, team 1 at gameplay indexes 0-4, team 2 at 5-9, the spectators (Spectator) at 10-14. The host
// answers the held HelloReqs with these slots and runs the pre-game.
type StartGame struct {
	Roster []RosterEntry `json:"roster"`
}

// GameReady says the level is loaded and the GMS port is bound.
type GameReady struct {
	Port int `json:"port"`
}

// AddPlayer says a HelloReq matched a roster entry.
type AddPlayer struct {
	User  int64 `json:"user"`
	Index int   `json:"index"`
}

// DisconnectPlayer says a player's GMS connection ended.
type DisconnectPlayer struct {
	User   int64  `json:"user"`
	Reason string `json:"reason"`
}

// Phases of reportGMSUpdate.
const (
	PhaseLoading  = "loading"
	PhasePreGame  = "preGame"
	PhaseInGame   = "inGame"
	PhasePostGame = "postGame"
)

// GMSUpdate is the heartbeat (every 5 s) and the phase report.
type GMSUpdate struct {
	Phase     string  `json:"phase"`
	ElapsedMs int64   `json:"elapsedMs"`
	Players   []int64 `json:"players"` // users connected to the GMS
}

// PlayerFinish is one player's result. Stats keys: docs/HOST_CONTROL.md (won, team, kills, deaths,
// assists, creeps, gold, level, bestStreak, doubleKills, tripleKills, tetraKills, omniKills,
// durationMs, noun). Rewards is optional (B7c): the host's own proposal; dgserver decides the rewards
// either way and answers with PlayerRewards.
type PlayerFinish struct {
	User    int64            `json:"user"`
	Stats   map[string]int64 `json:"stats,omitempty"`
	Rewards *Rewards         `json:"rewards,omitempty"`
}

// Rewards is what a player gets for a game, as the post-game's C1 04 carries it (MATCH_FLOW.md *Match
// end and post-game (D8-static)*: loot bag asset id, Destiny, the bag points, at most 4 items, the FTUE
// byte) plus the crafting currency the duplicates give (B7c).
type Rewards struct {
	LootBag        uint32       `json:"lootBag"`
	Destiny        uint32       `json:"destiny"`
	CC             uint32       `json:"cc"`
	Items          []RewardItem `json:"items"`
	FTUE           bool         `json:"ftue"`
	BaseRoll       int          `json:"baseRoll"`
	WinPoints      int          `json:"winPoints"`
	WinAltPoints   int          `json:"winAltPoints"`
	FirstWinPoints int          `json:"firstWinPoints"`
	// Karma (D8c) is the karma part of C1 04 as dgserver counted it into the bag score: present in a matchmade
	// game's playerRewards (decided after the host's playerKarma), absent otherwise (omitted).
	Karma *Karma `json:"karma,omitempty"`
}

// Karma is C1 04's karma part (MATCH_FLOW.md *Surrender and the karma vote (D8b-core)* §4): +0x18 the bag
// points for karma, +0x1C/+0x20/+0x24 the karma votes the player got from its party, its team and the enemy team.
type Karma struct {
	Points int `json:"points"`
	Party  int `json:"party"`
	Team   int `json:"team"`
	Enemy  int `json:"enemy"`
}

// RewardItem is one item of C1 04: {catalogID, isDuplicate, duplicationAlternate}.
type RewardItem struct {
	CatalogID uint32 `json:"catalogId"`
	Duplicate bool   `json:"duplicate"`
	Alternate uint32 `json:"alternate"`
}

// PlayerRewards (server -> host, B7c) answers a finished game for one player: the rewards dgserver
// decided and stored, for the host's C1 04. Sent once per roster player who joined, after finishGame.
type PlayerRewards struct {
	User    int64   `json:"user"`
	Rewards Rewards `json:"rewards"`
}

// PlayerKarma (host -> server, D8c) is one player's karma votes in a matchmade game's post-game: the votes the
// other players' C1 03 masks gave it, by the voter's relation (party, team, enemy; the host's postgame TallyKarma).
// The host sends one per player in the post-game once the vote has closed (KarmaVotingDuration + grace after C1 02,
// so after finishGame); dgserver then decides that player's loot bag with the karma points and answers
// playerRewards (with Karma). A practice game has no karma vote and no playerKarma.
type PlayerKarma struct {
	User  int64 `json:"user"`
	Party int   `json:"party"`
	Team  int   `json:"team"`
	Enemy int   `json:"enemy"`
}

// FinishGame is the match result.
type FinishGame struct {
	WinnerTeam int    `json:"winnerTeam"` // 0 = none
	DurationMs int64  `json:"durationMs"`
	Reason     string `json:"reason"`
}

// GMSError is an error report; Fatal means the host gives up: since H5 the match ends as aborted
// (Outcome.Error) unless finishGame came first, and a host that hasn't exited within the exit
// timeout is killed.
type GMSError struct {
	Code  int    `json:"code"`
	Text  string `json:"text"`
	Fatal bool   `json:"fatal"`
}

// reportGMSError's codes (H5; gms/core/control/messages.h kErr*, the same numbers and names;
// docs/HOST_CONTROL.md *A frozen host (H5)*).
const (
	ErrGMSEndpoint   = 1 // the GMS port could not be bound (gms_script's host mode, the host DLL since PORTSTUCK; fatal, exit code 2)
	ErrTickStalled   = 2 // the host's watchdog: no server tick for [host] watchdog_s (fatal; the host exits with code 6)
	ErrStepFailing   = 3 // a step of the host's frame threw in every run for [host] step_fail_s (fatal; exit code 7)
	ErrStepException = 4 // a step of the host's frame threw; that run was skipped and the tick went on (not fatal)
	ErrLevelLoad     = 5 // the host DLL's level did not load (PORTSTUCK; fatal, exit code 2, but not the port's fault)
)

// ErrorName names a reportGMSError code as both sides do: "GMS endpoint", "tick stalled", "step
// failing", "step exception", "level load"; "error" for any other.
func ErrorName(code int) string {
	switch code {
	case ErrGMSEndpoint:
		return "GMS endpoint"
	case ErrTickStalled:
		return "tick stalled"
	case ErrStepFailing:
		return "step failing"
	case ErrStepException:
		return "step exception"
	case ErrLevelLoad:
		return "level load"
	}
	return "error"
}

// LogoutGMS is the host's last line before it exits.
type LogoutGMS struct{}

// RemovePlayer says a player left on the Blaze side.
type RemovePlayer struct {
	User   int64  `json:"user"`
	Reason string `json:"reason"`
}

// AbortGame tells the host to end the match without a result and exit.
type AbortGame struct {
	Reason string `json:"reason"`
}

// AgentHello is an agent's first line (H2). Name identifies the host machine in the logs and in
// Match.Site; Nonce (32 hex digits, fresh per connection) is the agent's challenge to the server.
type AgentHello struct {
	Name  string `json:"name"`
	Nonce string `json:"nonce"`
	Build string `json:"build"`
}

// AgentChallenge answers AgentHello: the server's own fresh nonce, and its proof that it knows the
// key (agentProof over both nonces), so an agent never works for a server without the key.
type AgentChallenge struct {
	Nonce string `json:"nonce"`
	Proof string `json:"proof"`
}

// AgentAuth is the agent's proof (agentProof over both nonces, its name and the three fields here)
// and what it offers: Bind is the address its hosts' GMS ports bind (DGGMS_BIND's host), Public the
// address clients are told (NotifyGameSetup's HNET), Ports its GMS port range ("10042-10051").
type AgentAuth struct {
	Proof  string `json:"proof"`
	Bind   string `json:"bind"`
	Public string `json:"public"`
	Ports  string `json:"ports"`
}

// AgentReady says the agent is registered. Addr is the agent's address as the server sees it: the
// hosts it starts must dial in from that address.
type AgentReady struct {
	Addr string `json:"addr"`
}

// AgentStatus is the agent's heartbeat (every 5 s): the matches whose hosts run there.
type AgentStatus struct {
	Hosts []int64 `json:"hosts"`
}

// LaunchHost asks an agent to start a host for a match: DGGMS_MATCH, DGGMS_TOKEN and the port of
// DGGMS_BIND (on the agent's bind address). DGGMS_CONTROL is the address the agent itself dialed.
type LaunchHost struct {
	Match int64  `json:"match"`
	Token string `json:"token"`
	Port  int    `json:"port"`
}

// HostStarted answers LaunchHost: the process runs (PID on the agent's machine).
type HostStarted struct {
	Match int64 `json:"match"`
	PID   int   `json:"pid"`
}

// HostFailed answers LaunchHost: no process was started.
type HostFailed struct {
	Match int64  `json:"match"`
	Error string `json:"error"`
}

// KillHost asks the agent to kill a match's host.
type KillHost struct {
	Match int64 `json:"match"`
}

// HostExited says a match's host has exited (Code is its exit code).
type HostExited struct {
	Match int64 `json:"match"`
	Code  int   `json:"code"`
}

func (Hello) Type() string            { return THello }
func (CreateGame) Type() string       { return TCreateGame }
func (GameReady) Type() string        { return TGameReady }
func (AddPlayer) Type() string        { return TAddPlayer }
func (DisconnectPlayer) Type() string { return TDisconnectPlayer }
func (GMSUpdate) Type() string        { return TReportGMSUpdate }
func (PlayerFinish) Type() string     { return TPlayerFinish }
func (FinishGame) Type() string       { return TFinishGame }
func (GMSError) Type() string         { return TReportGMSError }
func (LogoutGMS) Type() string        { return TLogoutGMS }
func (RemovePlayer) Type() string     { return TRemovePlayer }
func (AbortGame) Type() string        { return TAbortGame }
func (PlayerRewards) Type() string    { return TPlayerRewards }
func (PlayerKarma) Type() string      { return TPlayerKarma }
func (UpdateRoster) Type() string     { return TUpdateRoster }
func (StartGame) Type() string        { return TStartGame }
func (AgentHello) Type() string       { return TAgentHello }
func (AgentChallenge) Type() string   { return TAgentChallenge }
func (AgentAuth) Type() string        { return TAgentAuth }
func (AgentReady) Type() string       { return TAgentReady }
func (AgentStatus) Type() string      { return TAgentStatus }
func (LaunchHost) Type() string       { return TLaunchHost }
func (HostStarted) Type() string      { return THostStarted }
func (HostFailed) Type() string       { return THostFailed }
func (KillHost) Type() string         { return TKillHost }
func (HostExited) Type() string       { return THostExited }

// Encode returns msg as one line: {"t":"<type>",<fields>}\n, the fields in struct order, map keys
// sorted, strings as raw UTF-8 (only '"', '\\', control characters, U+2028 and U+2029 escaped; the
// C++ writer does the same, so both sides write identical lines).
func Encode(msg Message) ([]byte, error) {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(msg); err != nil {
		return nil, err
	}
	fields := bytes.TrimRight(body.Bytes(), "\n")
	var b bytes.Buffer
	b.WriteString(`{"t":"`)
	b.WriteString(msg.Type()) // plain ASCII names
	b.WriteByte('"')
	if len(fields) > 2 { // not "{}"
		b.WriteByte(',')
		b.Write(fields[1:])
	} else {
		b.WriteByte('}')
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// ErrUnknownType is returned by Decode for a well-formed line with an unknown "t".
var ErrUnknownType = errors.New("gmshost: unknown message type")

// messageTypes are the decodable messages, one zero value each, by "t".
var messageTypes = func() map[string]Message {
	out := map[string]Message{}
	for _, m := range []Message{Hello{}, CreateGame{}, GameReady{}, AddPlayer{}, DisconnectPlayer{}, GMSUpdate{},
		PlayerFinish{}, FinishGame{}, GMSError{}, LogoutGMS{}, RemovePlayer{}, AbortGame{}, PlayerRewards{},
		PlayerKarma{}, UpdateRoster{}, StartGame{}, AgentHello{}, AgentChallenge{}, AgentAuth{}, AgentReady{}, AgentStatus{}, LaunchHost{}, HostStarted{},
		HostFailed{}, KillHost{}, HostExited{}} {
		out[m.Type()] = m
	}
	return out
}()

// Decode parses one line (without or with its newline). Unknown fields are ignored, so either side
// can add fields first. The message comes back as its value type (Hello, not *Hello), so callers
// switch on values.
func Decode(line []byte) (Message, error) {
	var head struct {
		T *string `json:"t"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, fmt.Errorf("gmshost: bad line: %w", err)
	}
	if head.T == nil {
		return nil, errors.New(`gmshost: line without "t"`)
	}
	zero, ok := messageTypes[*head.T]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownType, *head.T)
	}
	p := reflect.New(reflect.TypeOf(zero)) // a *T for the T registered under this "t"
	if err := json.Unmarshal(line, p.Interface()); err != nil {
		return nil, fmt.Errorf("gmshost: bad %s: %w", *head.T, err)
	}
	return p.Elem().Interface().(Message), nil
}

// LineReader reads control lines of at most MaxLine bytes.
type LineReader struct{ s *bufio.Scanner }

// NewLineReader reads lines from r.
func NewLineReader(r io.Reader) *LineReader {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), MaxLine+2) // + "\r\n"
	return &LineReader{s: s}
}

// Next returns the next non-empty line, without "\r\n". At the end it returns io.EOF (or the read
// error; bufio.ErrTooLong for a line over MaxLine).
func (r *LineReader) Next() ([]byte, error) {
	for r.s.Scan() {
		line := bytes.TrimRight(r.s.Bytes(), "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(line) > MaxLine {
			return nil, bufio.ErrTooLong
		}
		return line, nil
	}
	if err := r.s.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}
