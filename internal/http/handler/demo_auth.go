package handler

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/demoaccess"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

const demoSessionCookie = "forgeai_demo_session"

type DemoAuthHandler struct {
	auth              *usecase.DemoAccessUseCase
	enabled           bool
	requireCloudflare bool
	attempts          *demoLoginLimiter
}

func NewDemoAuthHandler(auth *usecase.DemoAccessUseCase, enabled, requireCloudflare bool) *DemoAuthHandler {
	return &DemoAuthHandler{
		auth: auth, enabled: enabled, requireCloudflare: requireCloudflare,
		attempts: newDemoLoginLimiter(5, 15*time.Minute),
	}
}

func (h *DemoAuthHandler) Enabled() bool { return h.enabled }

func (h *DemoAuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if _, err := h.UserFromRequest(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.renderLoginPage(w, http.StatusOK, false, "")
}

func (h *DemoAuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		writeDemoJSON(w, http.StatusConflict, map[string]any{"error": "demo authentication is disabled"})
		return
	}
	input := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{}
	isForm := strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded")
	if isForm {
		if err := r.ParseForm(); err == nil {
			input.Username = r.FormValue("username")
			input.Password = r.FormValue("password")
		}
	} else {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		_ = dec.Decode(&input)
	}
	if input.Username == "" || input.Password == "" {
		if isForm {
			h.renderLoginPage(w, http.StatusBadRequest, true, input.Username)
		} else {
			writeDemoJSON(w, http.StatusBadRequest, map[string]any{"error": "username and password are required"})
		}
		return
	}
	accessEmail := h.accessEmail(r)
	if h.requireCloudflare && accessEmail == "" {
		if isForm {
			h.renderLoginPage(w, http.StatusForbidden, true, input.Username)
		} else {
			writeDemoJSON(w, http.StatusForbidden, map[string]any{"error": "Cloudflare Access identity is required"})
		}
		return
	}
	key := r.RemoteAddr + "|" + strings.ToLower(accessEmail)
	if !h.attempts.Allow(key, time.Now()) {
		w.Header().Set("Retry-After", "900")
		if isForm {
			h.renderLoginPage(w, http.StatusTooManyRequests, true, input.Username)
		} else {
			writeDemoJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many login attempts"})
		}
		return
	}
	user, token, expiresAt, err := h.auth.Authenticate(r.Context(), input.Username, input.Password, accessEmail)
	if err != nil {
		h.attempts.Fail(key, time.Now())
		if isForm {
			h.renderLoginPage(w, http.StatusUnauthorized, true, input.Username)
		} else {
			writeDemoJSON(w, http.StatusUnauthorized, map[string]any{"error": "login failed"})
		}
		return
	}
	h.attempts.Clear(key)
	http.SetCookie(w, &http.Cookie{
		Name: demoSessionCookie, Value: token, Path: "/", Expires: expiresAt,
		MaxAge: int(time.Until(expiresAt).Seconds()), HttpOnly: true,
		Secure: requestIsHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
	if isForm {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeDemoJSON(w, http.StatusOK, demoAuthResponse(true, user))
}

func (h *DemoAuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		writeDemoJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	user, err := h.UserFromRequest(r)
	if err != nil {
		writeDemoJSON(w, http.StatusUnauthorized, map[string]any{"enabled": true, "error": "authentication required"})
		return
	}
	writeDemoJSON(w, http.StatusOK, demoAuthResponse(true, user))
}

func (h *DemoAuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(demoSessionCookie)
	if cookie != nil {
		_ = h.auth.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: demoSessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: requestIsHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *DemoAuthHandler) UserFromRequest(r *http.Request) (demoaccess.User, error) {
	if !h.enabled {
		return demoaccess.User{}, nil
	}
	cookie, err := r.Cookie(demoSessionCookie)
	if err != nil {
		return demoaccess.User{}, demoaccess.ErrInvalidCredentials
	}
	accessEmail := h.accessEmail(r)
	if h.requireCloudflare && accessEmail == "" {
		return demoaccess.User{}, demoaccess.ErrInvalidCredentials
	}
	return h.auth.CurrentUser(r.Context(), cookie.Value, accessEmail)
}

func (h *DemoAuthHandler) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := h.UserFromRequest(r); err != nil {
			writeDemoJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *DemoAuthHandler) RequirePage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := h.UserFromRequest(r); err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *DemoAuthHandler) accessEmail(r *http.Request) string {
	if !h.requireCloudflare {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email"))
}

func demoAuthResponse(enabled bool, u demoaccess.User) map[string]any {
	return map[string]any{"enabled": enabled, "user": map[string]any{
		"username": u.Username, "email": u.Email, "company": u.Company, "expires_at": u.ExpiresAt,
	}}
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func writeDemoJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *DemoAuthHandler) renderLoginPage(w http.ResponseWriter, status int, failed bool, username string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = demoLoginPage.Execute(w, struct {
		Failed   bool
		Username string
	}{failed, username})
}

var demoLoginPage = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>ForgeAI Customer Demo</title><style>
:root{font-family:Inter,"Noto Sans JP",system-ui,sans-serif;color:#172033;background:#f7f8fb}*{box-sizing:border-box}
body{min-height:100vh;margin:0;display:grid;place-items:center;padding:24px;background:radial-gradient(circle at 10% 10%,#dbeafe 0,transparent 32%),radial-gradient(circle at 90% 85%,#ede9fe 0,transparent 30%),#f7f8fb}
main{width:min(100%,430px);padding:38px;border:1px solid #e2e7ef;border-radius:22px;background:#fff;box-shadow:0 24px 70px #0f172a1f}
.mark{width:48px;height:48px;display:flex;align-items:end;justify-content:center;gap:4px;padding:11px;border-radius:14px;background:linear-gradient(145deg,#1d4ed8,#7c3aed)}.mark i{width:5px;border-radius:4px;background:#fff}.mark i:nth-child(1){height:10px;opacity:.65}.mark i:nth-child(2){height:20px}.mark i:nth-child(3){height:14px;opacity:.8}
.eyebrow{margin:24px 0 5px;color:#2563eb;font-size:.68rem;font-weight:800;letter-spacing:.14em}h1{margin:0;font-size:1.7rem;letter-spacing:-.04em}.lead,.note{color:#697386;font-size:.84rem;line-height:1.7}.lead{margin:12px 0 25px}.note{margin:22px 0 0;font-size:.7rem}
form{display:grid;gap:16px}label{display:grid;gap:7px;font-size:.78rem;font-weight:650}input{width:100%;padding:12px;border:1px solid #d8dee9;border-radius:10px;font:inherit}input:focus{border-color:#2563eb;outline:3px solid #2563eb1f}button{margin-top:4px;padding:12px;border:0;border-radius:10px;background:linear-gradient(135deg,#2563eb,#6d28d9);color:#fff;font:inherit;font-size:.84rem;font-weight:750;cursor:pointer}.error{margin:0;padding:10px 12px;border-radius:9px;background:#fef2f2;color:#b91c1c;font-size:.75rem}
</style></head><body><main><div class="mark" aria-hidden="true"><i></i><i></i><i></i></div><p class="eyebrow">CUSTOMER DEMO</p><h1>ForgeAI デモ</h1><p class="lead">TechVitからご案内した個別のユーザーIDとパスワードを入力してください。</p>
<form method="post" action="/auth/login"><label>ユーザーID<input name="username" autocomplete="username" required maxlength="64" value="{{.Username}}"></label><label>パスワード<input name="password" type="password" autocomplete="current-password" required></label>{{if .Failed}}<p class="error" role="alert">ログインできませんでした。ID、パスワード、利用期限をご確認ください。</p>{{end}}<button type="submit">デモを開く</button></form><p class="note">アカウントは申込者本人専用・期限付きです。共有はできません。</p></main></body></html>`))

type demoLoginAttempt struct {
	count      int
	windowEnds time.Time
}

type demoLoginLimiter struct {
	mu     sync.Mutex
	items  map[string]demoLoginAttempt
	limit  int
	window time.Duration
}

func newDemoLoginLimiter(limit int, window time.Duration) *demoLoginLimiter {
	return &demoLoginLimiter{items: make(map[string]demoLoginAttempt), limit: limit, window: window}
}

func (l *demoLoginLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, exists := l.items[key]
	return !exists || now.After(attempt.windowEnds) || attempt.count < l.limit
}

func (l *demoLoginLimiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, exists := l.items[key]
	if !exists || now.After(attempt.windowEnds) {
		attempt = demoLoginAttempt{windowEnds: now.Add(l.window)}
	}
	attempt.count++
	l.items[key] = attempt
	if len(l.items) > 10_000 {
		for itemKey, item := range l.items {
			if now.After(item.windowEnds) {
				delete(l.items, itemKey)
			}
		}
	}
}

func (l *demoLoginLimiter) Clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.items, key)
}
