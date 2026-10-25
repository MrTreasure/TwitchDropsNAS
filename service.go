package main

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var defaultSettings = M{"networkMode": "auto", "checkMinSeconds": 600, "checkMaxSeconds": 1200}
var defaultTask = M{"id": "war-thunder", "game": M{"id": nil, "name": "War Thunder", "slug": "war-thunder"}, "enabled": true, "priority": 100, "watchOnly": false, "autoChannels": false, "channels": []any{"warthunder_esports", "warthunder_esports_de", "warthunder_esports_fr", "warthunder_esports_ru", "warthunder_esports_pl"}, "goal": defaultGoal()}

type Pending struct {
	actor, userCode, uri, deviceCode, clientID string
	expires                                    float64
	interval                                   int
}
type Service struct {
	mu                       sync.Mutex
	scanMu                   sync.Mutex
	wg                       sync.WaitGroup
	store                    *Store
	book                     *Credentials
	net                      *Transport
	twitch                   *Twitch
	ctx                      context.Context
	cancel                   context.CancelFunc
	account                  M
	pending                  *Pending
	authError                any
	authCancel               context.CancelFunc
	authStarting             bool
	authRound                uint64
	authRequests             []float64
	current                  M
	revision, epoch          uint64
	state, reason            string
	nextSelection, nextWatch float64
	lastWatch                any
	watchReports             int
	channelErrors            int
	announcements            []any
	syncing                  atomic.Bool
	queued                   atomic.Bool
	automationChanged        chan struct{}
}

func newService(ctx context.Context) (*Service, error) {
	dir := env("TDM_DATA_DIR", "./data")
	store, e := openStore(dir)
	if e != nil {
		return nil, e
	}
	cleanup := true
	defer func() {
		if cleanup {
			store.db.Close()
		}
	}()
	bookPath := os.Getenv("TDM_PASSWORD_BOOK")
	book := &Credentials{path: filepath.Join(dir, "credentials.json")}
	if bookPath != "" {
		book.path = bookPath
		book.book = true
	}
	account, e := book.Read()
	if e != nil {
		return nil, e
	}
	proxy, e := newProxy()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{store: store, book: book, account: account, net: newTransport(store, proxy), ctx: ctx, cancel: cancel, state: "paused", reason: "尚未开始挂机", automationChanged: make(chan struct{}, 1)}
	s.twitch = &Twitch{s.net}
	settings := obj(store.Get("settings", M{}))
	for k, v := range defaultSettings {
		if settings[k] == nil {
			settings[k] = v
		}
	}
	store.Put("settings", settings)
	if number(store.Get("capabilitiesVersion", 0)) != 2 {
		store.PutMany(M{"inventoryFetchedAt": 0, "catalogueCache": M{}, "progress": M{"status": "unknown", "sampledAt": nil, "catalogueAvailable": false, "message": "等待重新核对账号进度读取能力"}, "capabilitiesVersion": 2})
	}
	tasks := store.Get("tasks", nil)
	if tasks == nil {
		tasks = []any{clone(defaultTask)}
	}
	for _, v := range list(tasks) {
		t := obj(v)
		if t["goal"] == nil {
			t["goal"] = defaultGoal()
		}
	}
	store.Put("tasks", tasks)
	s.watchReports = int(number(store.Get("watchReports", 0)))
	raw, readErr := os.ReadFile(env("TDM_ANNOUNCEMENTS_FILE", "catalogue/announcements.json"))
	if os.IsNotExist(readErr) && os.Getenv("TDM_ANNOUNCEMENTS_FILE") == "" {
		raw, readErr = bundled.ReadFile("catalogue/announcements.json")
	}
	if e := readErr; e == nil {
		if json.Unmarshal(raw, &s.announcements) != nil || len(s.announcements) > 100 {
			cancel()
			return nil, fail("公开活动说明文件无效")
		}
		for _, v := range s.announcements {
			for _, r := range list(obj(v)["rewards"]) {
				reward := obj(r)
				reward["imageUrl"] = rewardImageURL(reward["imageUrl"])
			}
		}
	} else if !os.IsNotExist(e) {
		cancel()
		return nil, e
	}
	if s.announcements == nil {
		s.announcements = []any{}
	}
	cleanup = false
	return s, nil
}
func (s *Service) launch(f func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if recover() != nil {
				s.store.Event("后台操作未完成，保留原有资料", "error")
			}
		}()
		f()
	}()
}
func (s *Service) start() error {
	if e := s.net.proxy.Start(); e != nil {
		return e
	}
	s.store.Event("Go 掉宝服务已启动，使用显式配置的连接策略", "info")
	s.launch(s.engine)
	s.launch(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.net.sampleProxy(s.ctx)
			}
		}
	})
	if !envBool("TDM_EXTERNAL_SCHEDULER") {
		s.launch(s.autoSync)
	}
	return nil
}
func (s *Service) close() {
	s.cancel()
	s.mu.Lock()
	if s.authCancel != nil {
		s.authCancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.net.close()
	s.store.db.Close()
}
func (s *Service) checkDelay() float64 {
	settings := obj(s.store.Get("settings", defaultSettings))
	lo, hi := number(settings["checkMinSeconds"]), number(settings["checkMaxSeconds"])
	return lo + rand.Float64()*(hi-lo)
}
func (s *Service) notifyAutomation() {
	select {
	case s.automationChanged <- struct{}{}:
	default:
	}
}
func (s *Service) autoSync() {
	if flag(s.store.Get("automationEnabled", true)) {
		s.safeScan(false)
	}
	for {
		enabled := flag(s.store.Get("automationEnabled", true))
		delay := s.checkDelay()
		var next any
		var wake <-chan time.Time
		timer := time.NewTimer(time.Duration(delay * 1e9))
		if enabled {
			next = unixNow() + delay
			wake = timer.C
		}
		s.store.Put("automationNextAt", next)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-s.automationChanged:
			timer.Stop()
			continue
		case <-wake:
			if flag(s.store.Get("automationEnabled", true)) {
				s.safeScan(false)
			}
		}
	}
}
func (s *Service) imageScopesLocked() []any {
	a, c := resolveImages(s.announcements, list(s.store.Get("campaigns", []any{})), obj(s.store.Get("rewardImageRegistry", M{})))
	return scopeDefinitions(a, c)
}
func (s *Service) imageImport(body M, save bool) (M, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scopeID, ok := body["scope"].(string)
	if !ok {
		return nil, fail("请选择图片所属活动")
	}
	var scope M
	for _, v := range s.imageScopesLocked() {
		a := obj(v)
		if text(a["id"]) == scopeID {
			scope = a
			break
		}
	}
	if scope == nil {
		return nil, fault("活动资料已变化，请重新选择活动", "image_scope_missing", 409)
	}
	plan, e := previewImport(scope, body["input"])
	if e != nil {
		return nil, e
	}
	if save {
		n, e := applyImport(s.store, plan)
		if e != nil {
			return nil, e
		}
		plan["updated"] = n
		s.store.Event("已导入活动奖励图片资料，仅保存原始 CDN 地址", "info")
	}
	return plan, nil
}
func (s *Service) snapshot(actor string) M {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(actor)
}
func (s *Service) snapshotLocked(actor string) M {
	var account, device any
	if s.account != nil {
		account = M{"user_id": s.account["user_id"], "login": s.account["login"], "validatedAt": s.account["validatedAt"], "expiresAt": s.account["expiresAt"]}
	}
	if p := s.pending; p != nil && p.actor == actor {
		device = M{"userCode": p.userCode, "verificationUri": p.uri, "expiresAt": iso(p.expires)}
	}
	session := s.store.Get("currentProgress", nil)
	sp := obj(session)
	ch := obj(s.current["channel"])
	stream := obj(ch["stream"])
	if s.account == nil || s.current == nil || session == nil || text(sp["accountId"]) != text(s.account["user_id"]) || text(sp["channelId"]) != text(ch["id"]) || text(sp["streamId"]) != text(stream["id"]) {
		session = nil
	}
	settings := s.store.Get("settings", defaultSettings)
	external := envBool("TDM_EXTERNAL_SCHEDULER")
	minSeconds, maxSeconds := obj(settings)["checkMinSeconds"], obj(settings)["checkMaxSeconds"]
	var nextMetadata any = s.store.Get("automationNextAt", nil)
	source := "service"
	if external {
		source = "external"
		minSeconds = envInt("TDM_EXTERNAL_CHECK_MIN_SECONDS", 600)
		maxSeconds = envInt("TDM_EXTERNAL_CHECK_MAX_SECONDS", 1200)
		nextMetadata = nil
	}
	var nextChannel any
	if s.nextSelection > unixNow() && flag(s.store.Get("running", false)) {
		nextChannel = s.nextSelection
	}
	games, names := map[string]bool{}, map[string]bool{}
	tasks := list(s.store.Get("tasks", []any{}))
	for _, v := range tasks {
		g := obj(obj(v)["game"])
		games[text(g["id"])] = true
		names[normalized(g["name"])] = true
	}
	announcements := []any{}
	for _, v := range s.announcements {
		a := obj(v)
		if (games[text(a["gameId"])] || names[normalized(a["gameName"])]) && timestamp(a["endsAt"]) > unixNow() {
			announcements = append(announcements, a)
		}
	}
	a, c := resolveImages(announcements, list(s.store.Get("campaigns", []any{})), obj(s.store.Get("rewardImageRegistry", M{})))
	scopes := []any{}
	for _, v := range s.imageScopesLocked() {
		scope := obj(v)
		scopes = append(scopes, M{"id": scope["id"], "name": scope["name"], "gameName": scope["gameName"], "rewardCount": len(list(scope["rewards"]))})
	}
	return M{"announcements": a, "rewardImageScopes": scopes, "schedule": M{"metadataSource": source, "metadataMinSeconds": minSeconds, "metadataMaxSeconds": maxSeconds, "nextMetadataAt": nextMetadata, "nextChannelAt": nextChannel}, "account": account, "authorization": M{"pending": s.pending != nil, "device": device, "error": s.authError, "reconnectRequired": s.account != nil && flag(s.store.Get("authorizationReconnectRequired", false))}, "settings": settings, "running": s.store.Get("running", false), "state": s.state, "reason": s.reason, "current": clone(s.current), "goals": s.goalStatesLocked(), "queueCompletedAt": s.store.Get("queueCompletedAt", nil), "tasks": tasks, "campaigns": c, "progress": s.store.Get("progress", M{"status": "unknown", "sampledAt": nil, "message": "连接 Twitch 后同步官方进度"}), "syncing": s.syncing.Load() || s.queued.Load(), "lastSync": s.store.Get("lastSync", nil), "lastWatchAt": s.lastWatch, "currentProgress": session, "watchReports": s.watchReports, "network": s.net.network(), "traffic": s.net.usage(), "events": s.store.Events(), "sampledAt": now()}
}
func (s *Service) goalStatesLocked() []any {
	campaigns := list(s.store.Get("campaigns", []any{}))
	readable := text(obj(s.store.Get("progress", M{}))["status"]) == "available"
	states := []any{}
	for _, v := range list(s.store.Get("tasks", []any{})) {
		states = append(states, evaluate(obj(v), campaigns, readable))
	}
	return states
}
func (s *Service) captureGoalsLocked() {
	tasks := list(s.store.Get("tasks", []any{}))
	campaigns := list(s.store.Get("campaigns", []any{}))
	catalogue := list(obj(s.store.Get("catalogueCache", M{}))["items"])
	known := map[string]bool{}
	for _, v := range campaigns {
		c := obj(v)
		if len(list(c["drops"])) > 0 {
			known[text(c["id"])] = true
		}
	}
	for _, v := range tasks {
		t := obj(v)
		complete := true
		for _, v := range catalogue {
			c := obj(v)
			if text(c["status"]) == "ACTIVE" && text(obj(c["game"])["id"]) == text(obj(t["game"])["id"]) && !known[text(c["id"])] {
				complete = false
			}
		}
		if flag(t["enabled"]) && complete {
			t["goal"] = capture(t, campaigns, now())
		}
	}
	s.store.Put("tasks", tasks)
}
func (s *Service) finishLocked() bool {
	enabled := map[string]bool{}
	for _, v := range list(s.store.Get("tasks", []any{})) {
		t := obj(v)
		if flag(t["enabled"]) {
			enabled[text(t["id"])] = true
		}
	}
	count := 0
	for _, v := range s.goalStatesLocked() {
		g := obj(v)
		if enabled[text(g["taskId"])] {
			count++
			if text(g["status"]) != "completed" {
				return false
			}
		}
	}
	if count > 0 && flag(s.store.Get("running", false)) {
		s.store.PutMany(M{"running": false, "queueCompletedAt": now()})
		s.current = nil
		s.state = "completed"
		s.reason = "所有启用游戏的本轮目标已由 Twitch 确认达标，挂机已结束；未领取奖励请手动领取"
		s.revision++
		s.store.Event("本轮所有目标已达标，已自动结束挂机；奖励仍需手动领取", "info")
		return true
	}
	return false
}
func (s *Service) recordError(e error) {
	if errorCode(e) == "state_changed" || s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = "error"
	s.reason = safeError(e)
	switch errorCode(e) {
	case "login_required", "verification_required", "watch_rejected", "integrity_required":
		if errorCode(e) == "login_required" {
			s.store.Put("authorizationReconnectRequired", true)
		}
		s.store.Put("running", false)
		s.current = nil
		s.revision++
	}
	s.store.Event(safeError(e), "warning")
}
func (s *Service) queueScan() {
	if !s.queued.CompareAndSwap(false, true) {
		return
	}
	s.launch(func() { defer s.queued.Store(false); s.safeScan(true) })
}
func (s *Service) safeScan(force bool) {
	ctx, cancel := context.WithTimeout(s.ctx, 180*time.Second)
	defer cancel()
	_, e := s.scan(ctx, force)
	if e != nil {
		s.recordError(e)
	}
}
func selectionMetadata(campaigns []any) []any {
	a := []any{}
	for _, v := range campaigns {
		c := obj(v)
		m := M{}
		for _, k := range []string{"id", "gameId", "startsAt", "endsAt", "linked", "allowChannels"} {
			m[k] = c[k]
		}
		a = append(a, m)
	}
	return a
}
func (s *Service) scan(ctx context.Context, force bool) (result M, err error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	s.syncing.Store(true)
	defer s.syncing.Store(false)
	s.store.Put("automationLastStarted", now())
	defer func() {
		status := "success"
		if err != nil {
			status = "error"
		}
		if p := recover(); p != nil {
			status = "error"
			err = fail("同步未完成，保留上次已确认资料")
		}
		s.store.PutMany(M{"automationLastFinished": now(), "automationLastStatus": status})
	}()
	if e := s.validateAccount(ctx); e != nil {
		if errorCode(e) == "no_account" {
			return M{"checked": true, "connected": false}, nil
		}
		return nil, e
	}
	s.mu.Lock()
	account := obj(clone(s.account))
	epoch := s.epoch
	tasks := list(s.store.Get("tasks", []any{}))
	beforeGoals := s.goalStatesLocked()
	beforeCampaigns := selectionMetadata(list(s.store.Get("campaigns", []any{})))
	s.mu.Unlock()
	for _, v := range tasks {
		t := obj(v)
		if flag(t["enabled"]) && text(obj(t["game"])["id"]) == "" {
			g, e := s.twitch.game(ctx, text(obj(t["game"])["name"]))
			if e != nil {
				return nil, e
			}
			t["game"] = g
		}
	}
	s.mu.Lock()
	if s.epoch != epoch {
		s.mu.Unlock()
		return M{"checked": false, "accountChanged": true}, nil
	}
	fresh := list(s.store.Get("tasks", []any{}))
	for _, v := range fresh {
		t := obj(v)
		for _, r := range tasks {
			checked := obj(r)
			if text(t["id"]) == text(checked["id"]) && text(obj(t["game"])["name"]) == text(obj(checked["game"])["name"]) {
				t["game"] = checked["game"]
			}
		}
	}
	tasks = fresh
	s.store.Put("tasks", tasks)
	old := obj(s.store.Get("progress", M{}))
	previous := list(s.store.Get("campaigns", []any{}))
	cache := obj(s.store.Get("catalogueCache", M{}))
	inventoryAt := number(s.store.Get("inventoryFetchedAt", 0))
	inventoryFailed := number(s.store.Get("inventoryFailedAt", 0))
	catalogueFailed := number(s.store.Get("catalogueFailedAt", 0))
	waiting := false
	for _, v := range s.goalStatesLocked() {
		if number(obj(v)["blocked"]) > 0 {
			waiting = true
		}
	}
	s.mu.Unlock()
	campaigns := map[string]M{}
	order := []string{}
	for _, v := range previous {
		c := obj(v)
		campaigns[text(c["id"])] = c
		order = append(order, text(c["id"]))
	}
	putCampaign := func(c M) {
		id := text(c["id"])
		if campaigns[id] == nil {
			order = append(order, id)
		}
		campaigns[id] = c
	}
	updates := M{}
	sampled := old["sampledAt"]
	inventoryOK := text(old["status"]) == "available"
	inventoryMessage := value(old, "message", "等待确认账号进度")
	period := 1800.0
	if waiting {
		period = 300
	}
	if (force || unixNow()-inventoryFailed >= 3600) && (force || unixNow()-inventoryAt >= period) {
		inventory, e := s.twitch.inventory(ctx, account)
		if e != nil {
			if errorCode(e) != "capability_unavailable" {
				return nil, e
			}
			inventoryOK = false
			inventoryMessage = safeError(e)
			inventoryFailed = unixNow()
			updates["inventoryFailedAt"] = inventoryFailed
		} else {
			sampled = now()
			for _, v := range list(inventory["dropCampaignsInProgress"]) {
				raw := obj(v)
				if text(raw["id"]) != "" {
					p := campaigns[text(raw["id"])]
					c := mergeProgress(cleanCampaign(raw, text(sampled)), p)
					c["metadataFetchedAt"] = p["metadataFetchedAt"]
					putCampaign(c)
				}
			}
			updates["inventoryFetchedAt"] = unixNow()
			updates["inventoryFailedAt"] = 0
			inventoryOK = true
			inventoryMessage = "已读取 Twitch 官方账号进度"
		}
	}
	catalogueOK := flag(old["catalogueAvailable"])
	catalogueMessage := old["catalogueMessage"]
	catalogue := []any{}
	var e error
	if !force && unixNow()-catalogueFailed < 3600 {
		catalogue = list(cache["items"])
		catalogueOK = false
	} else if !force && text(cache["accountId"]) == text(account["user_id"]) && unixNow()-number(cache["at"]) < 3600 {
		catalogue = list(cache["items"])
		catalogueOK = true
	} else {
		catalogue, e = s.twitch.campaigns(ctx, account)
		if e == nil {
			updates["catalogueCache"] = M{"accountId": account["user_id"], "at": unixNow(), "items": catalogue}
			updates["catalogueFailedAt"] = 0
			catalogueOK = true
			catalogueMessage = nil
		}
	}
	if e == nil {
		enabledGames, targets := map[string]bool{}, map[string]bool{}
		for _, v := range tasks {
			t := obj(v)
			if flag(t["enabled"]) {
				enabledGames[text(obj(t["game"])["id"])] = true
				for _, v := range list(obj(t["goal"])["rewards"]) {
					targets[text(obj(v)["campaignId"])] = true
				}
			}
		}
		candidates := []any{}
		for _, v := range catalogue {
			c := obj(v)
			if enabledGames[text(obj(c["game"])["id"])] && (text(c["status"]) == "ACTIVE" || targets[text(c["id"])]) {
				candidates = append(candidates, c)
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return timestamp(campaigns[text(obj(candidates[i])["id"])]["metadataFetchedAt"]) < timestamp(campaigns[text(obj(candidates[j])["id"])]["metadataFetchedAt"])
		})
		for _, v := range candidates[:min(24, len(candidates))] {
			id := text(obj(v)["id"])
			if !force && unixNow()-timestamp(campaigns[id]["metadataFetchedAt"]) < 21600 {
				continue
			}
			var detailed M
			detailed, e = s.twitch.details(ctx, account, id)
			if e != nil {
				break
			}
			if len(detailed) > 0 {
				c := mergeProgress(cleanCampaign(detailed, now()), campaigns[id])
				c["metadataFetchedAt"] = now()
				putCampaign(c)
			}
		}
	}
	if e != nil {
		if errorCode(e) != "capability_unavailable" {
			return nil, e
		}
		catalogueOK = false
		catalogueMessage = safeError(e)
		updates["catalogueFailedAt"] = unixNow()
	}
	var failed any
	if !inventoryOK {
		failed = old["failedAt"]
		if inventoryFailed > 0 {
			failed = iso(inventoryFailed)
		}
	}
	updates["progress"] = M{"status": map[bool]string{true: "available", false: "unavailable"}[inventoryOK], "sampledAt": sampled, "catalogueAvailable": catalogueOK, "catalogueMessage": catalogueMessage, "message": inventoryMessage, "failedAt": failed}
	s.mu.Lock()
	if s.epoch != epoch {
		s.mu.Unlock()
		return M{"checked": false, "accountChanged": true}, nil
	}
	current := obj(clone(s.current))
	revision := s.revision
	s.mu.Unlock()
	if len(current) > 0 {
		official, ce := s.twitch.current(ctx, account, obj(current["channel"]))
		if ce != nil && errorCode(ce) != "capability_unavailable" {
			return nil, ce
		}
		if ce == nil && len(official) > 0 {
			minutes := official["currentMinutesWatched"]
			if !validNumber(minutes) {
				minutes = nil
			}
			sampled := now()
			ch := obj(current["channel"])
			dropID := text(official["dropID"])
			updates["currentProgress"] = M{"dropId": dropID, "minutes": minutes, "sampledAt": sampled, "streamId": obj(ch["stream"])["id"], "accountId": account["user_id"], "channelId": ch["id"]}
			if minutes != nil {
				for _, c := range campaigns {
					for _, v := range list(c["drops"]) {
						d := obj(v)
						if text(d["id"]) == dropID {
							d["minutes"] = max(number(d["minutes"]), number(minutes))
							d["sampledAt"] = sampled
						}
					}
				}
			}
		}
	}
	updatedCampaigns := []any{}
	for _, id := range order {
		updatedCampaigns = append(updatedCampaigns, campaigns[id])
	}
	updates["campaigns"] = updatedCampaigns
	updates["lastSync"] = now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch != epoch {
		return M{"checked": false, "accountChanged": true}, nil
	}
	if s.revision != revision || !reflect.DeepEqual(s.current, current) {
		delete(updates, "currentProgress")
	}
	s.store.PutMany(updates)
	s.captureGoalsLocked()
	s.finishLocked()
	if !reflect.DeepEqual(beforeGoals, s.goalStatesLocked()) || !reflect.DeepEqual(beforeCampaigns, selectionMetadata(updatedCampaigns)) {
		s.nextSelection = 0
	}
	s.store.Event("完成一次资料检查；不可读取的字段保留未知", "info")
	return M{"checked": true, "connected": true, "progress": obj(updates["progress"])["status"]}, nil
}
