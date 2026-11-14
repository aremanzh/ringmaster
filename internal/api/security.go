package api

// Request guards. The API can add a project with any argv and start it, so
// every request passes three checks before it reaches a route:
//
//  1. Host: the Host header must be localhost, an IP address or this
//     machine's name. That stops DNS rebinding, where a page on a domain the
//     attacker controls re-points the domain at 127.0.0.1 to look same-origin.
//  2. Origin: a browser request that changes state (POST, PUT, DELETE) must
//     come from the page this server served. That stops any other web page
//     from driving the API with a cross-origin form post or fetch.
//  3. Token (only when RequireToken is set): the caller must present the
//     access token. `ringmaster serve` turns this on when it listens on
//     anything other than loopback.
//
// The WebSocket routes are GET requests; coder/websocket's Accept rejects a
// handshake whose Origin does not match the Host.

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

// tokenCookie carries the access token for browser sessions.
const tokenCookie = "ringmaster_token"

// maxBodyBytes bounds a JSON request body.
const maxBodyBytes = 1 << 20

// RequireToken makes every request present token, either as
// "Authorization: Bearer <token>" (the CLI) or as the session cookie a
// browser receives by opening /?token=<token> once. Call it before Handler.
func (s *Server) RequireToken(token string) { s.token = token }

// LoopbackOnly reports whether a listen address (host:port) is reachable
// from this machine only.
func LoopbackOnly(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// guard wraps the route table with the Host, Origin and token checks.
func (s *Server) guard(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusForbidden, errors.New("cross-origin request rejected: only the Ringmaster UI itself may change state"))
	}))
	checked := cop.Handler(next)

	names := map[string]bool{"localhost": true}
	if name, err := os.Hostname(); err == nil && name != "" {
		names[strings.ToLower(name)] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, names) {
			writeErr(w, http.StatusForbidden, fmt.Errorf("host %q is not allowed: open Ringmaster through localhost or an IP address", r.Host))
			return
		}
		if s.token != "" && !s.authorize(w, r) {
			return
		}
		checked.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether a Host header names this server directly: any
// IP literal, or one of names (lower-case). A DNS name outside names could be
// a rebinding domain.
func hostAllowed(hostport string, names map[string]bool) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return names[strings.ToLower(strings.TrimSuffix(host, "."))]
}

// authorize checks the access token. It returns true when the request may
// proceed; otherwise it has already written the response.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && s.tokenMatches(bearer) {
		return true
	}
	if c, err := r.Cookie(tokenCookie); err == nil && s.tokenMatches(c.Value) {
		return true
	}
	// First visit from a browser: trade ?token= for a session cookie, then
	// redirect so the token does not stay in the address bar or the history.
	if r.Method == http.MethodGet && s.tokenMatches(r.URL.Query().Get("token")) {
		http.SetCookie(w, &http.Cookie{
			Name: tokenCookie, Value: s.token, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
		u := *r.URL
		q := u.Query()
		q.Del("token")
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
		return false
	}
	writeErr(w, http.StatusUnauthorized, errors.New("access token required: open the URL that `ringmaster serve` printed (it ends in ?token=...)"))
	return false
}

func (s *Server) tokenMatches(got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}
