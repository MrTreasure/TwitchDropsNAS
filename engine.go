package main

import (
	"context"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
)

func activeCampaigns(task M, campaigns []any) []any {
	r := []any{}
	for _, v := range campaigns {
		c := obj(v)
		if text(c["gameId"]) == text(obj(task["game"])["id"]) && timestamp(c["startsAt"]) <= unixNow() && timestamp(c["endsAt"]) > unixNow() {
			r = append(r, c)
		}
	}
	return r
}
func (s *Service) selectChannel(ctx context.Context) error {
	s.mu.Lock()
	revision, epoch := s.revision, s.epoch
	account := obj(clone(s.account))
	tasks := []any{}
	for _, v := range list(s.store.Get("tasks", []any{})) {
		if flag(obj(v)["enabled"]) && flag(taskQueryPlan(obj(v), s.streamSchedules, unixNow(), s.current != nil && text(s.current["taskId"]) == text(obj(v)["id"]))["active"]) {
			tasks = append(tasks, v)
		}
	}
	plan := s.queryPlanLocked(unixNow())
	s.nextSelection = nextChannelCheck(plan, unixNow(), s.checkDelay(), 60+rand.Float64()*60, s.current != nil)
	campaignData := list(s.store.Get("campaigns", []any{}))
	readable := text(obj(s.store.Get("progress", M{}))["status"]) == "available"
	s.mu.Unlock()
	sort.SliceStable(tasks, func(i, j int) bool { return number(obj(tasks[i])["priority"]) > number(obj(tasks[j])["priority"]) })
	reason := "暂无启用的游戏任务"
	for _, v := range tasks {
		task := obj(v)
		goal := evaluate(task, campaignData, readable)
		switch text(goal["status"]) {
		case "completed":
			reason = "本轮目标已达标，正在检查其他游戏"
			continue
		case "waiting_claim":
			reason = "请先到 Twitch 库存领取前置奖励，领取后同步即可继续"
			continue
		case "expired":
			reason = "未完成目标的活动已结束，请调整目标；不会标记为完成"
			continue
		}
		campaigns := activeCampaigns(task, campaignData)
		if len(list(obj(task["goal"])["rewards"])) > 0 {
			filtered := []any{}
			for _, v := range campaigns {
				if contains(list(goal["runnableCampaignIds"]), text(obj(v)["id"])) {
					filtered = append(filtered, v)
				}
			}
			campaigns = filtered
			if len(campaigns) == 0 {
				reason = "目标资料或前置状态无法确认，等待官方同步"
				continue
			}
		}
		claimed := len(campaigns) > 0
		linked := true
		for _, v := range campaigns {
			c := obj(v)
			if c["linked"] == false {
				linked = false
			}
			drops := list(c["drops"])
			if len(drops) == 0 {
				claimed = false
			}
			for _, v := range drops {
				if !flag(obj(v)["claimed"]) {
					claimed = false
				}
			}
		}
		if claimed {
			reason = "本轮奖励已领取，等待新活动"
			continue
		}
		if !linked {
			reason = "请先在 Twitch 官方活动页关联此游戏账号"
			continue
		}
		if len(campaigns) == 0 && !flag(task["watchOnly"]) {
			reason = "尚未确认可参与活动；同步资料，或明确启用仅观看模式"
			continue
		}
		allowed := map[string]bool{}
		channels := []any{}
		for _, v := range campaigns {
			for _, v := range list(obj(v)["allowChannels"]) {
				login := strings.ToLower(text(obj(v)["login"]))
				if login != "" && !allowed[login] {
					allowed[login] = true
					channels = append(channels, login)
				}
			}
		}
		channels = append(channels, list(task["channels"])...)
		if flag(task["autoChannels"]) {
			a, e := s.twitch.directory(ctx, obj(task["game"]))
			if e != nil && errorCode(e) != "capability_unavailable" {
				return e
			}
			channels = append(channels, a...)
		}
		channels = unique(channels)
		found, e := s.twitch.channels(ctx, channels)
		if e != nil {
			return e
		}
		for _, v := range found {
			ch := obj(v)
			stream := obj(ch["stream"])
			if len(stream) == 0 || text(obj(stream["game"])["id"]) != text(obj(task["game"])["id"]) {
				continue
			}
			eligible := allowed[strings.ToLower(text(ch["login"]))]
			if !eligible && len(campaigns) > 0 {
				available, e := s.twitch.available(ctx, account, ch)
				if e != nil && errorCode(e) != "capability_unavailable" {
					return e
				}
				for _, a := range available {
					for _, c := range campaigns {
						if text(obj(a)["id"]) == text(obj(c)["id"]) {
							eligible = true
						}
					}
				}
			}
			if !eligible && !flag(task["watchOnly"]) {
				reason = "直播已找到，但无法确认掉宝资格；可在官方页面核对后启用仅观看模式"
				continue
			}
			s.mu.Lock()
			if !flag(s.store.Get("running", false)) || s.epoch != epoch || s.revision != revision || ctx.Err() != nil {
				s.mu.Unlock()
				return nil
			}
			oldCh := obj(s.current["channel"])
			changed := s.current == nil || text(oldCh["login"]) != text(ch["login"]) || text(s.current["taskId"]) != text(task["id"]) || text(obj(oldCh["stream"])["id"]) != text(stream["id"])
			eligibility := "unverified"
			if eligible {
				eligibility = "confirmed"
			}
			s.current = M{"taskId": task["id"], "game": task["game"], "channel": ch, "eligibility": eligibility, "selectedAt": now()}
			s.store.Put("watchResume", M{"accountId": account["user_id"], "current": s.current, "checkedAt": now()})
			if changed {
				s.nextWatch = unixNow()
				s.store.Event("已选择直播频道，开始等待观看上报间隔", "info")
			}
			s.state = "watching"
			s.reason = "直播播放链路运行中；奖励进度以 Twitch 返回为准"
			s.mu.Unlock()
			return nil
		}
		if !strings.HasPrefix(reason, "直播") {
			reason = "等待符合游戏和活动要求的频道开播"
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch == epoch && s.revision == revision {
		s.current = nil
		s.store.Put("watchResume", nil)
		s.state = "waiting"
		s.reason = reason
	}
	return nil
}
func (s *Service) engine() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, 180*time.Second)
			e := s.engineStep(ctx)
			cancel()
			if e != nil && s.ctx.Err() == nil {
				s.mu.Lock()
				s.channelErrors++
				s.nextSelection = unixNow() + min(900, 30*math.Pow(2, float64(min(5, s.channelErrors))))
				s.nextWatch = s.nextSelection
				s.mu.Unlock()
				s.recordError(e)
			}
		}
	}
}
func (s *Service) engineStep(ctx context.Context) (err error) {
	s.mu.Lock()
	startRevision, startEpoch := s.revision, s.epoch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.revision != startRevision || s.epoch != startEpoch {
			err = nil
		}
	}()
	defer func() {
		if recover() != nil {
			err = fail("挂机检查失败，已延长重试间隔")
		}
	}()
	s.mu.Lock()
	if !flag(s.store.Get("running", false)) {
		s.current = nil
		if s.store.Get("queueCompletedAt", nil) != nil {
			s.state = "completed"
			s.reason = "本轮所有目标已达标，挂机已结束；可手动领取奖励或设置下一轮目标"
		} else if s.state != "error" {
			s.state = "paused"
			s.reason = "挂机已暂停"
		}
		s.mu.Unlock()
		return nil
	}
	if s.account == nil {
		s.state = "login_required"
		s.reason = "请先连接 Twitch 账号"
		s.mu.Unlock()
		return nil
	}
	if s.finishLocked() {
		s.mu.Unlock()
		return nil
	}
	queryPlan := s.queryPlanLocked(unixNow())
	if !flag(queryPlan["active"]) {
		s.current = nil
		s.state = "scheduled_wait"
		s.reason = scheduledReason(queryPlan)
		s.nextSelection = number(queryPlan["nextWakeAt"])
		s.mu.Unlock()
		return nil
	}
	if s.net.blocked() {
		s.state = "cooldown"
		s.reason = "Twitch 限流或验证冷却中"
		s.mu.Unlock()
		return nil
	}
	selectNow := unixNow() >= s.nextSelection
	revision, epoch := s.revision, s.epoch
	s.mu.Unlock()
	if selectNow {
		if e := s.selectChannel(ctx); e != nil {
			return e
		}
	}
	s.mu.Lock()
	if s.current == nil || unixNow() < s.nextWatch || s.revision != revision || s.epoch != epoch || !flag(s.store.Get("running", false)) {
		s.mu.Unlock()
		return nil
	}
	account := obj(clone(s.account))
	current := obj(clone(s.current))
	s.nextWatch = unixNow() + watchIntervalSeconds
	s.mu.Unlock()
	channel, e := s.twitch.channel(ctx, text(obj(current["channel"])["login"]))
	if e != nil {
		return e
	}
	s.mu.Lock()
	if s.revision != revision || s.epoch != epoch || !flag(s.store.Get("running", false)) {
		s.mu.Unlock()
		return nil
	}
	if len(obj(channel["stream"])) == 0 || text(obj(obj(channel["stream"])["game"])["id"]) != text(obj(current["game"])["id"]) {
		s.current = nil
		s.store.Put("watchResume", nil)
		s.nextSelection = 0
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if e = s.twitch.watch(ctx, account, channel); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision == revision && s.epoch == epoch {
		s.lastWatch = now()
		s.watchReports++
		s.store.PutMany(M{"watchReports": s.watchReports, "playbackChecks": number(s.store.Get("playbackChecks", 0)) + 1, "lastPlaybackAt": s.lastWatch, "watchResume": M{"accountId": account["user_id"], "current": s.current, "checkedAt": s.lastWatch}})
		s.channelErrors = 0
	}
	return nil
}
