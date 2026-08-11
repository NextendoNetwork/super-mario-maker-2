package main

// Online GATES enforced at NEX login — the SAME rules as the old the previous stack infra:
//   - Compte Nextendo OBLIGATOIRE (requireAccount): aucune identité de compte -> refus.
//   - Online = comptes Nextendo UNIQUEMENT: un NSA de vraie console non lié / un serveur
//     compte injoignable -> refus (fail-CLOSED : un profil non-Nextendo n'entre jamais).
//   - #6 e-mail vérifié OBLIGATOIRE.
//   - #5 un seul endroit à la fois (présence RÉELLE via le monitoring).
//   - compte désactivé -> refus.
//
// The account server (nextendo-account) owns the gate logic; the auth server calls
// /internal/online-check + /api/nsa and rejects the LoginEx on a block. FAIL-OPEN on an
// online-check network error (a transient hiccup must never lock everyone out of online);
// FAIL-CLOSED on an unverifiable NSA identity.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	accountBaseURL = envOr("NEXTENDO_ACCOUNT_URL", "http://nextendo-account:8080")
	internalKey    = os.Getenv("NEXTENDO_INTERNAL_KEY")
	gateClient     = &http.Client{Timeout: 3 * time.Second}
)

// nextendoOnlineCheck asks nextendo-account whether this account may go online now.
// reason ∈ {"unknown","disabled","unverified","elsewhere",""}. Fail-OPEN on error.
func nextendoOnlineCheck(pid uint64, kind string) (bool, string) {
	body, _ := json.Marshal(map[string]any{"pid": pid, "kind": kind})
	req, err := http.NewRequest("POST", accountBaseURL+"/internal/online-check", bytes.NewReader(body))
	if err != nil {
		return true, ""
	}
	req.Header.Set("Content-Type", "application/json")
	if internalKey != "" {
		req.Header.Set("X-Internal-Key", internalKey)
	}
	resp, err := gateClient.Do(req)
	if err != nil {
		return true, "" // fail-open
	}
	defer resp.Body.Close()
	var out struct {
		Allow  bool   `json:"allow"`
		Reason string `json:"reason"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return true, ""
	}
	return out.Allow, out.Reason
}

// nsaStatus distingue les issues d'une résolution NSA -> compte Nextendo.
type nsaStatus int

const (
	nsaOK          nsaStatus = iota // NSA lié à un compte Nextendo (pid valide)
	nsaUnknown                      // 404 : aucun compte ne possède ce NSA -> profil non-Nextendo
	nsaUnreachable                  // serveur compte injoignable -> identité non vérifiable
)

var (
	nsaCacheMu sync.Mutex
	nsaCache   = map[uint64]uint64{}
	// nsaNegCache memoises FAILED resolutions (404 / unreachable). Without it every
	// login attempt carrying an unknown NSA id triggers a fresh outbound call to the
	// shared account service, so a flood of bogus ids on the (unauthenticated) auth
	// port turns into amplification against the service every game depends on. A short
	// TTL keeps it responsive right after an account is linked.
	nsaNegCache = map[uint64]nsaNegEntry{}
	// nsaInflight caps CONCURRENT /api/nsa calls: past the cap we answer "unreachable"
	// (fail-closed) instead of opening one more connection.
	nsaInflight = make(chan struct{}, nsaMaxInflight)
)

type nsaNegEntry struct {
	status nsaStatus
	at     time.Time
}

const (
	nsaNegTTL      = 60 * time.Second
	nsaMaxInflight = 16
	nsaNegCacheMax = 4096
)

// resolveNSAtoPID mappe un NSA id (baasUserID d'une vraie Switch) vers le PID du compte
// Nextendo : (pid, nsaOK) si lié, (0, nsaUnknown) si aucun compte ne le possède,
// (0, nsaUnreachable) si injoignable. Les résultats positifs sont cachés.
func resolveNSAtoPID(nsa uint64) (uint64, nsaStatus) {
	nsaCacheMu.Lock()
	if pid, ok := nsaCache[nsa]; ok {
		nsaCacheMu.Unlock()
		return pid, nsaOK
	}
	if neg, ok := nsaNegCache[nsa]; ok && time.Since(neg.at) < nsaNegTTL {
		nsaCacheMu.Unlock()
		return 0, neg.status // recently resolved as unknown/unreachable: no new call
	}
	nsaCacheMu.Unlock()

	// Cap concurrent outbound calls to the shared account service.
	select {
	case nsaInflight <- struct{}{}:
		defer func() { <-nsaInflight }()
	default:
		return 0, nsaUnreachable // saturated: fail-closed, without opening a connection
	}

	resp, err := gateClient.Get(fmt.Sprintf("%s/api/nsa?id=%d", accountBaseURL, nsa))
	if err != nil {
		rememberNSAFailure(nsa, nsaUnreachable)
		return 0, nsaUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		rememberNSAFailure(nsa, nsaUnknown)
		return 0, nsaUnknown
	}
	if resp.StatusCode != http.StatusOK {
		rememberNSAFailure(nsa, nsaUnreachable)
		return 0, nsaUnreachable
	}
	var out struct {
		PID uint64 `json:"pid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.PID == 0 {
		rememberNSAFailure(nsa, nsaUnreachable)
		return 0, nsaUnreachable
	}
	nsaCacheMu.Lock()
	nsaCache[nsa] = out.PID
	delete(nsaNegCache, nsa) // the account was just linked: a stale negative must not survive
	nsaCacheMu.Unlock()
	return out.PID, nsaOK
}

// rememberNSAFailure memoises a failed resolution for nsaNegTTL. The table is bounded:
// a flood of bogus ids must not grow it without end (we clear it wholesale at the cap —
// entries are only worth 60s anyway).
func rememberNSAFailure(nsa uint64, st nsaStatus) {
	nsaCacheMu.Lock()
	if len(nsaNegCache) >= nsaNegCacheMax {
		nsaNegCache = map[uint64]nsaNegEntry{}
	}
	nsaNegCache[nsa] = nsaNegEntry{status: st, at: time.Now()}
	nsaCacheMu.Unlock()
}
