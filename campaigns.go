package main

import "context"

// Browsing reads only the account's already fetched catalogue and inventory.
// It never starts a task, refreshes Twitch, or changes the queue's goals.
func (s *Service) campaignDirectoryLocked() []any {
	rows := []any{}
	if s.account == nil {
		return rows
	}
	index := map[string]int{}
	put := func(c M, source string, sampled any) {
		id, gameID, gameName := text(c["id"]), text(c["gameId"]), text(c["gameName"])
		if id == "" || gameID == "" || gameName == "" {
			return
		}
		row := M{"id": id, "name": c["name"], "gameId": gameID, "gameName": gameName, "startsAt": c["startsAt"], "endsAt": c["endsAt"], "status": c["status"], "linked": c["linked"], "rewardCount": len(list(c["drops"])), "detailsAvailable": len(list(c["drops"])) > 0, "sampledAt": sampled, "source": source}
		if i, ok := index[id]; ok {
			rows[i] = row
		} else {
			index[id] = len(rows)
			rows = append(rows, row)
		}
	}
	cache := obj(s.store.Get("catalogueCache", M{}))
	if text(cache["accountId"]) == text(s.account["user_id"]) {
		for _, v := range list(cache["items"]) {
			put(cleanCampaign(obj(v), ""), "catalogue", cache["at"])
		}
	}
	for _, v := range list(s.store.Get("campaigns", []any{})) {
		c := obj(v)
		put(c, "inventory_or_details", c["sampledAt"])
	}
	return rows
}

// Prepare official reward choices for a draft. Only known campaign IDs are
// accepted. One bounded detail request is made on explicit selection, not per row.
func (s *Service) previewCampaign(ctx context.Context, body M) error {
	id, ok := body["campaignId"].(string)
	if !ok || id == "" || len(id) > 200 {
		return fail("请选择活动列表中的掉宝活动")
	}
	if !s.scanMu.TryLock() {
		return fault("正在同步官方资料，请稍后选择活动", "sync_in_progress", 409)
	}
	defer s.scanMu.Unlock()
	s.mu.Lock()
	if s.account == nil {
		s.mu.Unlock()
		return fault("请先连接 Twitch 账号", "no_account", 409)
	}
	found := false
	for _, v := range s.campaignDirectoryLocked() {
		if text(obj(v)["id"]) == id {
			found = true
		}
	}
	if !found {
		s.mu.Unlock()
		return fault("活动不在当前账号的官方列表中，请先同步资料", "unknown_campaign", 409)
	}
	for _, v := range list(s.store.Get("campaigns", []any{})) {
		c := obj(v)
		if text(c["id"]) == id && len(list(c["drops"])) > 0 && unixNow()-timestamp(c["sampledAt"]) < 21600 {
			s.mu.Unlock()
			return nil
		}
	}
	if unixNow()-number(s.store.Get("campaignPreviewLastAttemptAt", 0)) < 10 {
		s.mu.Unlock()
		return fault("活动资料请求过于频繁，请稍候再试", "cooldown", 429)
	}
	s.store.Put("campaignPreviewLastAttemptAt", unixNow())
	account, epoch := obj(clone(s.account)), s.epoch
	s.mu.Unlock()
	raw, err := s.twitch.details(ctx, account, id)
	if err != nil {
		return err
	}
	if text(raw["id"]) != id || text(obj(raw["game"])["id"]) == "" || len(list(raw["timeBasedDrops"])) == 0 {
		return fault("Twitch 暂未返回此活动的奖励资料，请到官方活动页核对", "capability_unavailable", 409)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch != epoch || text(s.account["user_id"]) != text(account["user_id"]) {
		return fault("账号已变更，请重新选择活动", "state_changed", 409)
	}
	campaigns := list(s.store.Get("campaigns", []any{}))
	fresh := cleanCampaign(raw, now())
	fresh["metadataFetchedAt"] = now()
	for i, v := range campaigns {
		if text(obj(v)["id"]) == id {
			campaigns[i] = mergeProgress(fresh, obj(v))
			s.store.Put("campaigns", campaigns)
			return nil
		}
	}
	s.store.Put("campaigns", append(campaigns, fresh))
	return nil
}
