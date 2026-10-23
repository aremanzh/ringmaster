package core

import "fmt"

// Kind classifies an Engine error so a transport can map it to the right
// status: HTTP handlers translate it to a status code, the Wails GUI simply
// shows the message. Every error the Engine returns is a *Error.
type Kind int

const (
	// KindConflict — the request is well-formed but conflicts with current
	// state (task already running, duplicate ID, supervisor refusal). HTTP 409.
	KindConflict Kind = iota
	// KindNotFound — the named project, task, or action does not exist. HTTP 404.
	KindNotFound
	// KindBadRequest — the request is invalid (missing dir, bad field). HTTP 400.
	KindBadRequest
	// KindInternal — the operation half-succeeded or an internal step failed
	// (config saved but rebuild failed). HTTP 500.
	KindInternal
)

// Error is the single error type the Engine returns. Kind drives status
// mapping; Msg is shown to the user verbatim.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func conflict(format string, args ...any) *Error {
	return &Error{Kind: KindConflict, Msg: fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) *Error {
	return &Error{Kind: KindNotFound, Msg: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) *Error {
	return &Error{Kind: KindBadRequest, Msg: fmt.Sprintf(format, args...)}
}

func internal(format string, args ...any) *Error {
	return &Error{Kind: KindInternal, Msg: fmt.Sprintf(format, args...)}
}
