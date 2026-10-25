package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Transport struct {
	mu                     sync.Mutex
	meterMu                sync.Mutex
	store                  *Store
	proxy                  *Proxy
	client, proxyClient    *http.Client
	route                  string
	lastDirectProbe        float64
	directSuccesses        int
	proxyBytes             int64
	lastError, lastSuccess any
	blockedUntil           float64
	slots                  chan struct{}
}

func newTransport(store *Store, p *Proxy) *Transport {
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, ForceAttemptHTTP2: true}
	redirect := func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	t := &Transport{store: store, proxy: p, route: "unknown", client: &http.Client{Transport: tr, CheckRedirect: redirect}, slots: make(chan struct{}, 2)}
	if p.URL != "" {
		pt := tr.Clone()
		u, e := url.Parse(p.URL)
		must(e)
		pt.Proxy = http.ProxyURL(u)
		t.proxyClient = &http.Client{Transport: pt, CheckRedirect: redirect}
	}
	return t
}
func (t *Transport) usage() M {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	n := time.Now().In(zone)
	midnight := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, zone).Unix()
	basis := "all-response-estimate"
	if t.proxy.Controller != 0 {
		basis = "proxy-tunnel-bytes; direct-response-estimate"
	}
	return M{"hourBytes": t.store.TrafficSince(unixNow()-3600, ""), "dayBytes": t.store.TrafficSince(float64(midnight), ""), "proxyDayBytes": t.store.TrafficSince(float64(midnight), "proxy"), "directDayBytes": t.store.TrafficSince(float64(midnight), "direct"), "timeZone": "Asia/Shanghai", "basis": basis}
}
func (t *Transport) network() M {
	t.mu.Lock()
	defer t.mu.Unlock()
	var cooldown any
	if t.blockedUntil > unixNow() {
		cooldown = iso(t.blockedUntil)
	}
	return M{"route": t.route, "proxy": t.proxy.Label, "lastSuccessAt": t.lastSuccess, "lastError": t.lastError, "cooldownUntil": cooldown}
}
func (t *Transport) blocked() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.blockedUntil > unixNow()
}
func (t *Transport) reset() { t.mu.Lock(); t.route = "unknown"; t.lastDirectProbe = 0; t.mu.Unlock() }
func (t *Transport) sampleProxy(ctx context.Context) {
	if t.proxy.Controller == 0 {
		return
	}
	t.meterMu.Lock()
	defer t.meterMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+text(t.proxy.Controller)+"/connections", nil)
	if e != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+t.proxy.Secret)
	r, e := t.client.Do(req)
	if e != nil {
		return
	}
	defer r.Body.Close()
	var m M
	if json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&m) != nil {
		return
	}
	if !validNumber(m["uploadTotal"]) || !validNumber(m["downloadTotal"]) {
		return
	}
	total := int64(number(m["uploadTotal"]) + number(m["downloadTotal"]))
	diff := max(int64(0), total-t.proxyBytes)
	t.proxyBytes = total
	if diff > 0 {
		t.store.Traffic(unixNow(), "proxy", diff)
	}
}
func (t *Transport) send(ctx context.Context, method, destination, route string, headers M, form url.Values, payload any, cap int, timeout time.Duration) (int, M, error) {
	client := t.client
	if route == "proxy" {
		if t.proxyClient == nil {
			return 0, nil, fault("直连不可用且尚未配置代理，请检查连接设置", "proxy_unavailable", 503)
		}
		if e := t.proxy.Start(); e != nil {
			return 0, nil, fault("指定代理无法启动，请检查私有部署配置", "proxy_unavailable", 503)
		}
		client = t.proxyClient
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	contentType := ""
	requestBytes := 0
	if payload != nil {
		b := bytesJSON(payload)
		body = bytes.NewReader(b)
		requestBytes = len(b)
		contentType = "application/json"
	} else if form != nil {
		b := form.Encode()
		body = strings.NewReader(b)
		requestBytes = len(b)
		contentType = "application/x-www-form-urlencoded"
	}
	req, e := http.NewRequestWithContext(ctx, method, destination, body)
	if e != nil {
		return 0, nil, e
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, text(v))
	}
	response, e := client.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer response.Body.Close()
	var raw []byte
	if method != "HEAD" {
		raw, e = io.ReadAll(io.LimitReader(response.Body, int64(cap)+1))
	}
	if route == "direct" || t.proxy.Controller == 0 {
		t.store.Traffic(unixNow(), route, int64(len(raw)+4096+requestBytes))
	}
	if e != nil {
		return 0, nil, e
	}
	if len(raw) > cap {
		return 0, nil, fault("上游响应超过大小限制，已停止读取", "response_limit", 502)
	}
	if response.StatusCode == 429 {
		t.mu.Lock()
		t.blockedUntil = unixNow() + 900
		t.mu.Unlock()
		return 0, nil, fault("Twitch 请求限流，暂停 15 分钟；不切换线路重试", "rate_limited", 429)
	}
	if (response.StatusCode == 403 || response.StatusCode == 503) && strings.Contains(response.Header.Get("Content-Type"), "text/html") {
		t.mu.Lock()
		t.blockedUntil = unixNow() + 900
		t.mu.Unlock()
		return 0, nil, fault("Twitch 返回验证或限制页面，请人工检查", "verification_required", 409)
	}
	result := M{}
	if len(raw) > 0 {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			result = M{"text": string(raw)}
		} else if m, ok := value.(map[string]any); ok {
			result = m
		} else {
			result = M{"_invalidShape": true}
		}
	}
	return response.StatusCode, result, nil
}
func (t *Transport) request(ctx context.Context, method, destination string, headers M, form url.Values, payload any, cap int) (int, M, error) {
	u, e := url.Parse(destination)
	playback := e == nil && twitchPlaybackDestination(method, u)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || !(u.Hostname() == "id.twitch.tv" || u.Hostname() == "gql.twitch.tv" || u.Hostname() == "spade.twitch.tv" || playback) {
		return 0, nil, fault("此服务只允许访问必要的 Twitch 接口", "invalid_destination", 400)
	}
	if strings.Contains(u.Path, "/integrity") || !playback && (strings.Contains(u.Path, ".m3u8") || strings.Contains(u.Path, ".mp4") || strings.Contains(u.Path, ".ts")) {
		return 0, nil, fault("服务禁止下载视频音频正文或请求验证绕过接口", "media_blocked", 400)
	}
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
	if t.blocked() {
		return 0, nil, fault("Twitch 限制冷却中，请稍后再试", "rate_limited", 429)
	}
	t.sampleProxy(ctx)
	mode := text(obj(t.store.Get("settings", M{}))["networkMode"])
	t.mu.Lock()
	route := t.route
	if mode == "proxy" {
		route = "proxy"
	} else if mode == "direct" {
		route = "direct"
	}
	probe := mode == "auto" && (route == "unknown" || unixNow()-t.lastDirectProbe > 1800)
	if probe {
		t.lastDirectProbe = unixNow()
	}
	t.mu.Unlock()
	if probe {
		_, _, e := t.send(ctx, "GET", "https://id.twitch.tv/oauth2/validate", "direct", nil, nil, nil, 4096, 5*time.Second)
		t.mu.Lock()
		if e == nil {
			t.directSuccesses++
			if route == "unknown" || t.directSuccesses >= 2 {
				route = "direct"
			}
		} else if errorCode(e) == "internal_error" {
			t.directSuccesses = 0
			route = "proxy"
		} else {
			t.mu.Unlock()
			return 0, nil, e
		}
		t.mu.Unlock()
	}
	if route == "unknown" {
		route = "proxy"
	}
	if cap <= 0 {
		cap = 2097152
	}
	status, result, e := t.send(ctx, method, destination, route, headers, form, payload, cap, 15*time.Second)
	t.mu.Lock()
	if e == nil {
		t.route = route
		t.lastSuccess = now()
		t.lastError = nil
	} else if errorCode(e) == "internal_error" {
		if mode == "auto" && route == "direct" {
			t.route = "proxy"
		}
		t.lastError = "网络连接失败，稍后重试；只使用显式配置的线路"
		e = fault(text(t.lastError), "network_error", 503)
	}
	t.mu.Unlock()
	t.sampleProxy(ctx)
	return status, result, e
}
func (t *Transport) close() {
	t.client.CloseIdleConnections()
	if t.proxyClient != nil {
		t.proxyClient.CloseIdleConnections()
	}
	t.proxy.Close()
}
