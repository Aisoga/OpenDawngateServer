package accounts

import (
	"strings"
	"unicode/utf8"
)

// Blaze error codes a login is refused with: the 16-bit Fire codes (the reply header's error field) of
// docs/data/blaze_components.txt. The client shows the BlazeErrors line of the full code, (code << 16) | 1 below
// 0x4000 and code << 16 for the framework codes, in its LOGIN ERROR panel. Codes without a line show "*** [<full code>]" until the added locale package gives
// them one.
const (
	CodeInvalidUser     uint16 = 0x000B // AUTH_ERR_INVALID_USER: an unknown login name (the same text as a wrong password)
	CodeInvalidPassword uint16 = 0x000C // AUTH_ERR_INVALID_PASSWORD: a wrong or empty password
	CodeExists          uint16 = 0x000F // AUTH_ERR_EXISTS: a new login name that is taken (the website)
	CodeInvalidName     uint16 = 0x0016 // AUTH_ERR_INVALID_EMAIL: a new login name with other characters than allowed
	CodeDeactivated     uint16 = 0x0029 // AUTH_ERR_DEACTIVATED: an account its player deleted
	CodeBanned          uint16 = 0x002B // AUTH_ERR_BANNED
	CodeCodeUsed        uint16 = 0x0033 // AUTH_ERR_CODE_ALREADY_USED: an invite code used up (the website)
	CodeInvalidCode     uint16 = 0x0034 // AUTH_ERR_INVALID_CODE: an unknown, expired or revoked invite code
	CodeUserInactive    uint16 = 0x0065 // AUTH_ERR_USER_INACTIVE: an account the operator disabled
	CodeTooShort        uint16 = 0x00CA // AUTH_ERR_FIELD_TOO_SHORT: a new login name or password too short (no text yet)
	CodeSystem          uint16 = 0x4001 // ERR_SYSTEM: the account store failed
	CodeTooLong         uint16 = 0x400C // ERR_STRING_TOO_LONG: a login name or password too long
	CodeBusy            uint16 = 0x4072 // ERR_SERVER_BUSY: too many sign-ups from the address today (no text yet)
)

// The login-name and password rules (the proposal, the server's own: the client checks nothing). They apply to new
// accounts and new passwords; an account of the earlier builds whose name breaks them still logs in.
const (
	MinName     = 3  // characters
	MaxName     = 32 // characters
	MinPassword = 8  // characters
	// MaxPassword is in bytes (UTF-8): the client keeps a 63-byte copy of the password in its login data, so a
	// longer one is refused rather than risk a client that sends only those 63.
	MaxPassword = 63
	// MaxInput is the longest login name looked up at all (bytes); anything longer is refused before any work.
	MaxInput = 1024
)

// nameChar reports whether r may be in a login name: A-Z a-z 0-9 . _ - @ (the @ keeps e-mail-named accounts valid).
func nameChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' || r == '@'
}

// CheckName checks a new login name (already trimmed) against the rules: 0 if it is fine, else the code to refuse it
// with (CodeTooShort, CodeTooLong, CodeInvalidName).
func CheckName(name string) uint16 {
	for _, r := range name {
		if !nameChar(r) {
			return CodeInvalidName
		}
	}
	switch n := len(name); { // ASCII only by now: bytes = characters
	case n < MinName:
		return CodeTooShort
	case n > MaxName:
		return CodeTooLong
	}
	return 0
}

// CheckPassword checks a new password: 0 if it is fine, else CodeInvalidPassword (empty), CodeTooShort or
// CodeTooLong. Any characters are allowed (the client sends what is typed, as UTF-8).
func CheckPassword(pw string) uint16 {
	switch {
	case pw == "":
		return CodeInvalidPassword
	case len(pw) > MaxPassword:
		return CodeTooLong
	case utf8.RuneCountInString(pw) < MinPassword:
		return CodeTooShort
	}
	return 0
}

// The display-name rules (the website's registration lets the player choose it; a login's sign-up still makes
// it from the login name, store.PersonaName): the characters and the length store.PersonaName makes, so a chosen name
// looks like the made ones in the client (the main menu's name, the lobby's invite by name).
const (
	MinDisplay = 3
	MaxDisplay = 16 // store.MaxPersonaName
)

// displayChar reports whether r may be in a display name: A-Z a-z 0-9 _ -.
func displayChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

// CheckDisplayName checks a chosen display name (already trimmed): 0 if it is fine, else CodeInvalidName,
// CodeTooShort or CodeTooLong.
func CheckDisplayName(name string) uint16 {
	for _, r := range name {
		if !displayChar(r) {
			return CodeInvalidName
		}
	}
	switch n := len(name); {
	case n < MinDisplay:
		return CodeTooShort
	case n > MaxDisplay:
		return CodeTooLong
	}
	return 0
}

// NormalizeName is the login name as it is stored and compared: without surrounding blanks (the store compares it
// case-insensitively).
func NormalizeName(name string) string { return strings.TrimSpace(name) }
