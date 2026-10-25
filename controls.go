package main

import (
	"context"
	"strings"
)

func validateGoal(value any, old, game M, campaigns []any) (M, error) {
	previous := obj(old["goal"])
	if len(previous) == 0 {
		previous = defaultGoal()
	}
	same := old != nil && text(obj(old["game"])["id"]) == text(game["id"])
	if value == nil {
		if same {
			return previous, nil
		}
		return defaultGoal(), nil
	}
	g, ok := value.(map[string]any)
	if !ok || (g["mode"] != "all" && g["mode"] != "selected") {
		return nil, fail("奖励目标设置无效")
	}
	if _, ok := valueOrFalse(g["reset"]).(bool); !ok {
		return nil, fail("重新选择目标的设置无效")
	}
	if g["mode"] == "all" {
		if same && previous["mode"] == "all" && !flag(g["reset"]) {
			return previous, nil
		}
		return defaultGoal(), nil
	}
	refs, ok := g["rewards"].([]any)
	if !ok || len(refs) < 1 || len(refs) > 500 {
		return nil, fail("请选择 1–500 个奖励目标")
	}
	known := map[string]bool{}
	key := func(c, d string) string { return c + "\x00" + d }
	for _, v := range campaigns {
		c := obj(v)
		if text(c["gameId"]) == text(game["id"]) {
			for _, v := range list(c["drops"]) {
				known[key(text(c["id"]), text(obj(v)["id"]))] = true
			}
		}
	}
	if same {
		for _, v := range list(previous["rewards"]) {
			r := obj(v)
			known[key(text(r["campaignId"]), text(r["dropId"]))] = true
		}
	}
	selected := []any{}
	seen := map[string]bool{}
	for _, v := range refs {
		r, ok := v.(map[string]any)
		if !ok {
			return nil, fail("奖励标识无效")
		}
		c, ok1 := r["campaignId"].(string)
		d, ok2 := r["dropId"].(string)
		if !ok1 || !ok2 || c == "" || d == "" || len(c) > 200 || len(d) > 200 {
			return nil, fail("奖励标识无效")
		}
		k := key(c, d)
		if !known[k] {
			return nil, fail("所选奖励不属于此游戏或尚未读取，请先同步官方资料")
		}
		if !seen[k] {
			seen[k] = true
			selected = append(selected, M{"campaignId": c, "dropId": d})
		}
	}
	return M{"mode": "selected", "rewards": selected, "lockedAt": now()}, nil
}
func valueOrFalse(v any) any {
	if v == nil {
		return false
	}
	return v
}
func (s *Service) control(body M) error {
	running, ok := body["running"].(bool)
	if !ok {
		return fail("挂机开关格式无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if running {
		if s.account == nil {
			return fail("请先连接 Twitch 账号")
		}
		enabled := map[string]bool{}
		for _, v := range list(s.store.Get("tasks", []any{})) {
			t := obj(v)
			if flag(t["enabled"]) {
				enabled[text(t["id"])] = true
			}
		}
		count, complete := 0, true
		for _, v := range s.goalStatesLocked() {
			g := obj(v)
			if enabled[text(g["taskId"])] {
				count++
				if g["status"] != "completed" {
					complete = false
				}
			}
		}
		if count > 0 && complete {
			return fail("本轮目标已全部完成，请先在任务设置中选择下一轮目标")
		}
	}
	s.store.PutMany(M{"queueCompletedAt": nil, "running": running})
	s.revision++
	s.current = nil
	s.nextSelection = 0
	if running {
		s.state = "waiting"
		s.reason = "正在选择任务"
		s.store.Event("已开启挂机", "info")
	} else {
		s.state = "paused"
		s.reason = "挂机已暂停"
		s.store.Event("已暂停挂机", "info")
	}
	return nil
}
func (s *Service) settings(body M) error {
	mode := text(body["networkMode"])
	if mode != "auto" && mode != "direct" && mode != "proxy" {
		return fail("网络模式无效")
	}
	if mode == "proxy" && s.net.proxy.URL == "" {
		return fail("请先通过 TDM_PROXY_URL 或 mihomo 环境变量配置代理")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := obj(s.store.Get("settings", defaultSettings))
	for k := range defaultSettings {
		if v, ok := body[k]; ok {
			updated[k] = v
		}
	}
	lo, hi := updated["checkMinSeconds"], updated["checkMaxSeconds"]
	if !integer(lo) || !integer(hi) || number(lo) < 120 || number(lo) > number(hi) || number(hi) > 10800 {
		return fail("随机检查间隔需为 2–180 分钟，最短不能大于最长")
	}
	s.store.Put("settings", updated)
	s.nextSelection = unixNow() + s.checkDelay()
	s.notifyAutomation()
	s.net.reset()
	s.store.Event("已更新连接设置；按低流量方式持续运行，不设流量额度", "info")
	return nil
}
func (s *Service) saveTask(ctx context.Context, id string, body M) error {
	name, ok := body["gameName"].(string)
	name = strings.TrimSpace(name)
	if !ok || len([]rune(name)) < 1 || len([]rune(name)) > 100 {
		return fail("请填写 Twitch 游戏名称")
	}
	channels, ok := value(body, "channels", []any{}).([]any)
	if !ok || len(channels) > 20 {
		return fail("频道最多 20 个，只填写频道名，不填写网址")
	}
	for i, v := range channels {
		c, ok := v.(string)
		if !ok || !channelPattern.MatchString(c) {
			return fail("频道最多 20 个，只填写频道名，不填写网址")
		}
		channels[i] = strings.ToLower(c)
	}
	for _, k := range []string{"enabled", "watchOnly", "autoChannels"} {
		if _, ok := body[k].(bool); !ok {
			return fail("任务开关格式无效")
		}
	}
	if !integer(body["priority"]) || number(body["priority"]) > 100 {
		return fail("优先级需为 0–100 的整数")
	}
	s.mu.Lock()
	tasks := list(s.store.Get("tasks", []any{}))
	var old M
	for _, v := range tasks {
		t := obj(v)
		if text(t["id"]) == id {
			old = t
		}
	}
	if id != "" && old == nil {
		s.mu.Unlock()
		return fault("游戏任务不存在", "not_found", 404)
	}
	if old == nil && len(tasks) >= 12 {
		s.mu.Unlock()
		return fail("最多配置 12 个游戏任务")
	}
	revision := s.revision
	game := obj(old["game"])
	s.mu.Unlock()
	if text(game["name"]) != name {
		g, e := s.twitch.game(ctx, name)
		if e != nil {
			return e
		}
		game = g
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision != s.revision {
		return fault("任务已被另一位管理员修改，请刷新", "state_changed", 409)
	}
	tasks = list(s.store.Get("tasks", []any{}))
	for _, v := range tasks {
		t := obj(v)
		if text(t["id"]) != id && strings.EqualFold(text(obj(t["game"])["name"]), text(game["name"])) {
			return fail("该游戏已在任务列表中")
		}
	}
	goal, e := validateGoal(body["goal"], old, game, list(s.store.Get("campaigns", []any{})))
	if e != nil {
		return e
	}
	newID := id
	if newID == "" {
		newID = randomID(16)
	}
	updated := M{"id": newID, "game": game, "channels": unique(channels), "goal": goal}
	for _, k := range []string{"enabled", "priority", "watchOnly", "autoChannels"} {
		updated[k] = body[k]
	}
	if old != nil {
		for i, v := range tasks {
			if text(obj(v)["id"]) == id {
				tasks[i] = updated
			}
		}
	} else {
		tasks = append(tasks, updated)
	}
	s.store.Put("tasks", tasks)
	s.revision++
	s.current = nil
	s.nextSelection = 0
	s.captureGoalsLocked()
	s.store.Put("queueCompletedAt", nil)
	s.store.Event("已保存游戏任务及本轮奖励目标", "info")
	return nil
}
func (s *Service) deleteTask(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := []any{}
	for _, v := range list(s.store.Get("tasks", []any{})) {
		if text(obj(v)["id"]) != id {
			tasks = append(tasks, v)
		}
	}
	s.revision++
	s.store.Put("tasks", tasks)
	s.current = nil
	s.nextSelection = 0
	s.store.Event("已删除游戏任务", "info")
}
func (s *Service) automation(body M, write bool) (M, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	external := envBool("TDM_EXTERNAL_SCHEDULER")
	if write {
		if external {
			return nil, fault("请通过部署方的原生定时任务管理器控制此计划", "external_scheduler", 409)
		}
		enabled, ok := body["enabled"].(bool)
		if !ok {
			return nil, fail("enabled 必须是布尔值")
		}
		s.store.PutMany(M{"automationEnabled": enabled, "automationNextAt": nil})
		s.notifyAutomation()
	}
	settings := obj(s.store.Get("settings", defaultSettings))
	minSeconds, maxSeconds := settings["checkMinSeconds"], settings["checkMaxSeconds"]
	var next any = s.store.Get("automationNextAt", nil)
	var enabled any = s.store.Get("automationEnabled", true)
	source := "service"
	if external {
		source = "external"
		minSeconds = envInt("TDM_EXTERNAL_CHECK_MIN_SECONDS", 600)
		maxSeconds = envInt("TDM_EXTERNAL_CHECK_MAX_SECONDS", 1200)
		next = nil
		enabled = nil
	}
	return M{"source": source, "intervalMinSeconds": minSeconds, "intervalMaxSeconds": maxSeconds, "nextRunAt": next, "timeZone": "UTC", "enabled": enabled, "lastStartedAt": s.store.Get("automationLastStarted", nil), "lastFinishedAt": s.store.Get("automationLastFinished", nil), "lastStatus": s.store.Get("automationLastStatus", "unknown")}, nil
}
