package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Built assets and public documents only. No runtime data or credentials are embedded.
//
//go:embed ui/dist catalogue/*.json docs/openapi.json
var bundled embed.FS

type Web struct {
	webEnabled bool
	s          *Service
	token      string
	signingKey []byte
	uiDir      string
}

func newWeb(s *Service) (*Web, error) {
	w := &Web{s: s, uiDir: os.Getenv("TDM_UI_DIR"), webEnabled: env("TDM_WEB_UI", "true") != "false"}
	if envInt("TDM_HTTP_PORT", 8080) == 0 {
		return w, nil
	}
	token := os.Getenv("TDM_ADMIN_TOKEN")
	if token == "" {
		p := filepath.Join(env("TDM_DATA_DIR", "./data"), "admin-token")
		raw, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			token = randomID(32)
			if e = atomicPrivate(p, []byte(token)); e != nil {
				return nil, e
			}
		} else if e != nil {
			return nil, e
		} else {
			token = strings.TrimSpace(string(raw))
		}
	}
	if len(token) < 24 {
		return nil, errors.New("management token must contain at least 24 characters")
	}
	w.token = token
	h := sha256.Sum256([]byte(token))
	w.signingKey = h[:]
	return w, nil
}
func (w *Web) signature(payload string) string {
	h := hmac.New(sha256.New, w.signingKey)
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}
func (w *Web) validCookie(v string) bool {
	p := strings.Split(v, ".")
	if len(p) != 3 {
		return false
	}
	expires, e := strconv.ParseInt(p[0], 10, 64)
	n := time.Now().Unix()
	return e == nil && expires > n && expires <= n+86400 && hmac.Equal([]byte(p[2]), []byte(w.signature(p[0]+"."+p[1])))
}
func expectedOrigin(r *http.Request) string {
	if origin := os.Getenv("TDM_PUBLIC_ORIGIN"); origin != "" {
		return origin
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, e error) {
	status := 500
	code := "internal_error"
	message := safeError(e)
	var se *serviceError
	if errors.As(e, &se) {
		status = se.Status
		code = se.Code
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, M{"error": message, "code": code})
}
func readBody(r *http.Request) (M, error) {
	defer r.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(r.Body, 32769))
	if e != nil {
		return nil, fail("请求读取失败")
	}
	if len(raw) > 32768 {
		return nil, fault("请求超过大小限制", "request_limit", 413)
	}
	var m M
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, fail("请求需为 JSON 对象")
	}
	return m, nil
}
func (w *Web) handler(trusted bool) http.Handler {
	return http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(out, failInternal())
			}
		}()
		out.Header().Set("Cache-Control", "no-store")
		out.Header().Set("X-Content-Type-Options", "nosniff")
		out.Header().Set("Referrer-Policy", "no-referrer")
		out.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https://static-cdn.jtvnw.net https://images.twitch.tv https://static.twitchcdn.net https://staticfiles.warthunder.com https://patchnotes.cdn.gaijin.net https://warthunder.com https://static.encyclopedia.warthunder.com; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		p := r.URL.Path
		actor := "standalone-admin"
		if trusted {
			actor = truncate(r.Header.Get("X-Drops-Actor"), 100)
		}
		if p == "/health" && r.Method == "GET" {
			writeJSON(out, 200, M{"ok": true, "version": version})
			return
		}
		if !trusted {
			if strings.HasPrefix(p, "/api/") {
				origin := r.Header.Get("Origin")
				if origin != "" && origin != expectedOrigin(r) {
					writeError(out, fault("Origin mismatch", "forbidden", 403))
					return
				}
				if p == "/api/session" && r.Method == "POST" {
					body, e := readBody(r)
					if e != nil {
						writeError(out, e)
						return
					}
					token, ok := body["token"].(string)
					if !ok || !hmac.Equal([]byte(token), []byte(w.token)) {
						writeError(out, fault("Unauthorized", "unauthorized", 401))
						return
					}
					payload := strconv.FormatInt(time.Now().Unix()+86400, 10) + "." + randomID(16)
					http.SetCookie(out, &http.Cookie{Name: "tdm_session", Value: payload + "." + w.signature(payload), Path: "/", MaxAge: 86400, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: strings.HasPrefix(expectedOrigin(r), "https://")})
					writeJSON(out, 200, M{"authenticated": true})
					return
				}
				authorization := r.Header.Get("Authorization")
				bearer := strings.HasPrefix(authorization, "Bearer ") && hmac.Equal([]byte(strings.TrimPrefix(authorization, "Bearer ")), []byte(w.token))
				cookie, ce := r.Cookie("tdm_session")
				validCookie := ce == nil && w.validCookie(cookie.Value)
				if !bearer && !validCookie {
					writeError(out, fault("Unauthorized", "unauthorized", 401))
					return
				}
				if validCookie && !bearer && r.Method != "GET" && r.Method != "HEAD" && origin != expectedOrigin(r) {
					writeError(out, fault("Origin required", "forbidden", 403))
					return
				}
				p = strings.TrimPrefix(p, "/api")
				if p == "/session" {
					switch r.Method {
					case "GET":
						writeJSON(out, 200, M{"authenticated": true})
					case "DELETE":
						http.SetCookie(out, &http.Cookie{Name: "tdm_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
						writeJSON(out, 200, M{"authenticated": false})
					default:
						writeError(out, fault("Method not allowed", "method_not_allowed", 405))
					}
					return
				}
				if p == "/openapi.json" && r.Method == "GET" {
					raw, e := bundled.ReadFile("docs/openapi.json")
					if e != nil {
						writeError(out, e)
						return
					}
					out.Header().Set("Content-Type", "application/json")
					out.Write(raw)
					return
				}
			} else if (p == "/" || strings.HasPrefix(p, "/assets/")) && (r.Method == "GET" || r.Method == "HEAD") {
				if w.webEnabled {
					w.asset(out, r)
				} else {
					writeError(out, fault("Web UI disabled", "not_found", 404))
				}
				return
			} else {
				writeError(out, fault("Not found", "not_found", 404))
				return
			}
			if strings.HasPrefix(p, "/internal/") {
				writeError(out, fault("Not found", "not_found", 404))
				return
			}
		}
		var body M
		var e error
		if r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT" {
			body, e = readBody(r)
			if e != nil {
				writeError(out, e)
				return
			}
		}
		status := 200
		var result any
		switch {
		case p == "/snapshot" && r.Method == "GET":
			result = w.s.snapshot(actor)
		case p == "/auth/start" && r.Method == "POST":
			if actor == "" {
				e = fault("缺少已验证的管理身份", "unauthorized", 403)
			} else {
				result, e = w.s.beginAuth(r.Context(), actor, text(first(body["clientId"], deviceClient)))
			}
		case p == "/auth/cancel" && r.Method == "POST":
			e = w.s.cancelAuth(actor)
		case p == "/auth/disconnect" && r.Method == "POST":
			e = w.s.disconnect(r.Context())
		case p == "/control" && r.Method == "POST":
			e = w.s.control(body)
		case p == "/settings" && r.Method == "PATCH":
			e = w.s.settings(body)
		case p == "/tasks" && r.Method == "POST":
			e = w.s.saveTask(r.Context(), "", body)
		case strings.HasPrefix(p, "/tasks/") && r.Method == "PUT":
			e = w.s.saveTask(r.Context(), strings.TrimPrefix(p, "/tasks/"), body)
		case strings.HasPrefix(p, "/tasks/") && r.Method == "DELETE":
			w.s.deleteTask(strings.TrimPrefix(p, "/tasks/"))
		case p == "/sync" && r.Method == "POST":
			w.s.queueScan()
			status = 202
		case p == "/internal/scan" && trusted && r.Method == "POST":
			result, e = w.s.scan(r.Context(), false)
			if e != nil {
				w.s.recordError(e)
			}
		case p == "/automation" && (r.Method == "GET" || r.Method == "PATCH"):
			result, e = w.s.automation(body, r.Method == "PATCH")
		case (p == "/reward-images/preview" || p == "/reward-images/import") && r.Method == "POST":
			result, e = w.s.imageImport(body, p == "/reward-images/import")
		default:
			e = fault("Not found", "not_found", 404)
		}
		if e != nil {
			writeError(out, e)
			return
		}
		if result == nil {
			result = w.s.snapshot(actor)
		}
		writeJSON(out, status, result)
	})
}
func failInternal() error {
	return fault("操作失败，原有数据已保留", "internal_error", 500)
}
func (w *Web) asset(out http.ResponseWriter, r *http.Request) {
	name := "index.html"
	if r.URL.Path != "/" {
		name = strings.TrimPrefix(r.URL.Path, "/")
		if path.Clean(name) != name || path.Dir(name) != "assets" || strings.Contains(name, "\\") {
			writeError(out, fault("Not found", "not_found", 404))
			return
		}
	}
	var raw []byte
	var e error
	if w.uiDir != "" {
		file := filepath.Join(w.uiDir, filepath.FromSlash(name))
		resolved, re := filepath.EvalSymlinks(file)
		root, rr := filepath.EvalSymlinks(w.uiDir)
		if re != nil || rr != nil {
			writeError(out, fault("Not found", "not_found", 404))
			return
		}
		resolved, _ = filepath.Abs(resolved)
		root, _ = filepath.Abs(root)
		rel, re := filepath.Rel(root, resolved)
		if re != nil || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			writeError(out, fault("Not found", "not_found", 404))
			return
		}
		raw, e = os.ReadFile(resolved)
	} else {
		raw, e = fs.ReadFile(bundled, "ui/dist/"+name)
	}
	if e != nil {
		writeError(out, fault("Not found", "not_found", 404))
		return
	}
	if strings.HasPrefix(name, "assets/") {
		out.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	out.Header().Set("Content-Type", contentType)
	out.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	if r.Method != "HEAD" {
		_, _ = out.Write(raw)
	}
}
