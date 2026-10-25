package main

import (
	"context"
	"net/url"
	"regexp"
	"time"
)

var clientPattern = regexp.MustCompile(`^[A-Za-z0-9]{20,64}$`)
var codePattern = regexp.MustCompile(`^[A-Za-z0-9]{4,16}$`)

func (s *Service) beginAuth(ctx context.Context, actor, clientID string) (M, error) {
	s.mu.Lock()
	if s.pending != nil {
		if s.pending.actor != actor {
			s.mu.Unlock()
			return nil, fault("另一位管理员正在授权，请等待或取消当前授权", "authorization_busy", 409)
		}
		r := s.snapshotLocked(actor)
		s.mu.Unlock()
		return r, nil
	}
	if s.authStarting {
		s.mu.Unlock()
		return nil, fault("授权申请正在处理，请稍后读取状态", "authorization_busy", 409)
	}
	recent := []float64{}
	for _, t := range s.authRequests {
		if t > unixNow()-600 {
			recent = append(recent, t)
		}
	}
	s.authRequests = recent
	if len(recent) >= 4 {
		s.mu.Unlock()
		return nil, fault("授权请求过于频繁，请稍后重试", "local_rate_limit", 429)
	}
	if !clientPattern.MatchString(clientID) {
		s.mu.Unlock()
		return nil, fail("Twitch Client ID 格式无效")
	}
	s.authRequests = append(s.authRequests, unixNow())
	s.authStarting = true
	s.authRound++
	round := s.authRound
	authCtx, cancel := context.WithCancel(s.ctx)
	s.authCancel = cancel
	s.mu.Unlock()
	status, r, e := s.net.request(ctx, "POST", "https://id.twitch.tv/oauth2/device", nil, url.Values{"client_id": {clientID}, "scopes": {""}}, nil, 16384)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authRound != round {
		cancel()
		return nil, fault("授权状态已经改变", "state_changed", 409)
	}
	s.authStarting = false
	if e != nil {
		cancel()
		return nil, e
	}
	if status != 200 {
		cancel()
		return nil, fault("设备授权申请失败，可在高级设置填写自己的 Twitch 公共应用 Client ID", "device_auth_unavailable", 409)
	}
	uri, code := text(r["verification_uri"]), text(r["user_code"])
	u, ue := url.Parse(uri)
	if ue != nil || u.Scheme != "https" || u.Hostname() != "www.twitch.tv" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Path != "/activate" || !codePattern.MatchString(code) || text(r["device_code"]) == "" {
		cancel()
		return nil, fault("Twitch 返回了无法验证的授权地址", "device_auth_unavailable", 502)
	}
	expires := unixNow() + min(1800, max(1, number(value(r, "expires_in", 600))))
	p := &Pending{actor: actor, userCode: code, uri: uri, deviceCode: text(r["device_code"]), clientID: clientID, expires: expires, interval: max(5, int(number(value(r, "interval", 5))))}
	s.pending = p
	s.authError = nil
	s.store.Event("已申请 Twitch 设备授权，等待在官方页面确认", "info")
	s.launch(func() { s.pollAuth(authCtx, p, round) })
	return s.snapshotLocked(actor), nil
}
func waitContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *Service) pollAuth(ctx context.Context, p *Pending, round uint64) {
	failures, interval := 0, p.interval
	var final error
	defer func() {
		if recover() != nil {
			final = fail("授权信息保存失败，原有凭据已保留，请重试")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.authRound == round && s.pending == p {
			if final != nil && ctx.Err() == nil {
				s.authError = safeError(final)
				s.store.Event("Twitch 授权未完成，请查看账号连接状态", "warning")
			}
			s.pending = nil
		}
	}()
	for unixNow() < p.expires {
		if !waitContext(ctx, time.Duration(min(interval, 60))*time.Second) {
			return
		}
		status, r, e := s.net.request(ctx, "POST", "https://id.twitch.tv/oauth2/token", nil, url.Values{"client_id": {p.clientID}, "device_code": {p.deviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, nil, 16384)
		if e != nil {
			failures++
			if failures >= 3 || errorCode(e) == "rate_limited" || errorCode(e) == "verification_required" {
				final = e
				return
			}
			interval = min(60, interval+5)
			continue
		}
		message := text(first(r["message"], r["error"]))
		if message == "authorization_pending" {
			continue
		}
		if message == "slow_down" {
			interval += 5
			continue
		}
		if status != 200 || text(r["access_token"]) == "" {
			final = fail("授权被取消、失效或拒绝，请重新连接")
			return
		}
		verified, e := s.twitch.validate(ctx, text(r["access_token"]))
		if e != nil {
			final = e
			return
		}
		if text(verified["client_id"]) != p.clientID {
			final = fail("Twitch 授权应用不匹配")
			return
		}
		account := M{"access_token": r["access_token"], "refresh_token": r["refresh_token"], "client_id": verified["client_id"], "user_id": verified["user_id"], "login": verified["login"], "validatedAt": now(), "expiresAt": unixNow() + number(verified["expires_in"])}
		s.mu.Lock()
		if s.authRound != round || ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		if e = s.book.Write(account); e != nil {
			s.mu.Unlock()
			final = fail("授权信息保存失败，原有凭据已保留，请重试")
			return
		}
		s.account = account
		s.epoch++
		s.revision++
		s.nextSelection = 0
		s.current = nil
		s.store.PutMany(M{"authorizationReconnectRequired": false, "queueCompletedAt": nil, "campaigns": []any{}, "inventoryFetchedAt": 0, "inventoryFailedAt": 0, "catalogueFailedAt": 0, "catalogueCache": M{}, "currentProgress": nil, "progress": M{"status": "unknown", "sampledAt": nil, "message": "已连接，等待首次读取官方进度"}})
		s.pending = nil
		s.store.Event("Twitch 授权成功，登录凭据仅保存在当前部署的私有凭据存储", "info")
		s.mu.Unlock()
		s.queueScan()
		return
	}
	final = fail("授权码已过期，请重新连接")
}
func (s *Service) cancelAuth(actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil && s.pending.actor != actor {
		return fault("只能取消自己发起的授权", "forbidden", 403)
	}
	s.cancelAuthLocked()
	return nil
}
func (s *Service) cancelAuthLocked() {
	if s.authCancel != nil {
		s.authCancel()
	}
	s.authRound++
	s.authStarting = false
	s.pending = nil
}
func (s *Service) disconnect(ctx context.Context) error {
	s.mu.Lock()
	if e := s.book.Write(nil); e != nil {
		s.mu.Unlock()
		return e
	}
	account := obj(clone(s.account))
	s.revision++
	s.epoch++
	s.store.Put("running", false)
	s.current = nil
	s.cancelAuthLocked()
	s.account = nil
	s.authError = nil
	s.store.PutMany(M{"authorizationReconnectRequired": false, "campaigns": []any{}, "catalogueCache": M{}, "currentProgress": nil, "progress": M{"status": "unknown", "sampledAt": nil, "message": "账号已解除连接"}})
	s.state = "paused"
	s.reason = "账号已解除连接"
	epoch := s.epoch
	s.mu.Unlock()
	if len(account) > 0 {
		status, _, e := s.net.request(ctx, "POST", "https://id.twitch.tv/oauth2/revoke", nil, url.Values{"client_id": {text(account["client_id"])}, "token": {text(account["access_token"])}}, nil, 8192)
		if e != nil || status != 200 {
			s.mu.Lock()
			if s.epoch == epoch {
				s.authError = "已清除当前部署的登录；远端撤销未确认，请在 Twitch 账号设置中检查连接"
			}
			s.mu.Unlock()
		}
	}
	s.store.Event("已停止挂机并解除当前部署的 Twitch 连接", "info")
	return nil
}
func (s *Service) validateAccount(ctx context.Context) error {
	s.mu.Lock()
	if s.account == nil {
		s.mu.Unlock()
		return fault("请先连接 Twitch 账号", "no_account", 409)
	}
	account := obj(clone(s.account))
	epoch := s.epoch
	s.mu.Unlock()
	if unixNow()-timestamp(account["validatedAt"]) < 3600 && number(account["expiresAt"]) > unixNow()+300 && !flag(s.store.Get("authorizationReconnectRequired", false)) {
		return nil
	}
	verified, e := s.twitch.validate(ctx, text(account["access_token"]))
	if e != nil {
		if errorCode(e) != "login_required" || text(account["refresh_token"]) == "" {
			return e
		}
		status, r, err := s.net.request(ctx, "POST", "https://id.twitch.tv/oauth2/token", nil, url.Values{"client_id": {text(account["client_id"])}, "grant_type": {"refresh_token"}, "refresh_token": {text(account["refresh_token"])}}, nil, 16384)
		if err != nil {
			return err
		}
		if status != 200 || text(r["access_token"]) == "" {
			return fault("Twitch 登录已失效，请重新连接", "login_required", 401)
		}
		account["access_token"] = r["access_token"]
		if r["refresh_token"] != nil {
			account["refresh_token"] = r["refresh_token"]
		}
		verified, e = s.twitch.validate(ctx, text(account["access_token"]))
		if e != nil {
			return e
		}
	}
	if text(verified["user_id"]) != text(account["user_id"]) {
		return fault("Twitch 授权身份不匹配，请重新连接", "login_required", 401)
	}
	account["validatedAt"] = now()
	account["expiresAt"] = unixNow() + number(verified["expires_in"])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch != epoch {
		return fault("账号状态已经改变", "state_changed", 409)
	}
	if e = s.book.Write(account); e != nil {
		return e
	}
	s.account = account
	s.store.Put("authorizationReconnectRequired", false)
	return nil
}
