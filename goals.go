package main

import "sort"

func defaultGoal() M { return M{"mode": "all", "rewards": []any{}, "lockedAt": nil} }
func achieved(drop M) bool {
	return flag(drop["claimed"]) || flag(drop["claimable"]) || (validNumber(drop["minutes"]) && validNumber(drop["requiredMinutes"]) && number(drop["requiredMinutes"]) > 0 && number(drop["minutes"]) >= number(drop["requiredMinutes"]))
}
func capture(task M, campaigns []any, sampled string) M {
	goal := obj(task["goal"])
	if len(goal) == 0 {
		goal = defaultGoal()
	}
	if text(goal["mode"]) != "all" || len(list(goal["rewards"])) > 0 {
		return goal
	}
	rewards := []any{}
	for _, v := range campaigns {
		c := obj(v)
		if text(c["gameId"]) != text(obj(task["game"])["id"]) || timestamp(c["startsAt"]) > unixNow() || timestamp(c["endsAt"]) <= unixNow() {
			continue
		}
		for _, v := range list(c["drops"]) {
			d := obj(v)
			if text(d["id"]) != "" {
				rewards = append(rewards, M{"campaignId": c["id"], "dropId": d["id"]})
			}
		}
	}
	var at any
	if len(rewards) > 0 {
		at = sampled
	}
	return M{"mode": "all", "rewards": rewards, "lockedAt": at}
}
func dependencyState(node M, drops map[string]M, path map[string]bool) string {
	id := text(node["id"])
	if path[id] {
		return "unknown"
	}
	next := map[string]bool{}
	for k, v := range path {
		next[k] = v
	}
	next[id] = true
	states := []string{}
	for _, p := range list(node["prerequisites"]) {
		parent := drops[text(p)]
		switch {
		case parent == nil:
			states = append(states, "unknown")
		case flag(parent["claimed"]):
			continue
		case achieved(parent):
			states = append(states, "waiting_claim")
		default:
			states = append(states, dependencyState(parent, drops, next))
		}
	}
	if len(states) == 0 {
		return "ready"
	}
	for _, s := range states {
		if s == "ready" {
			return s
		}
	}
	for _, s := range states {
		if s == "unknown" {
			return s
		}
	}
	return "waiting_claim"
}
func evaluate(task M, campaigns []any, readable bool) M {
	refs := list(obj(task["goal"])["rewards"])
	catalogue := map[string]M{}
	for _, v := range campaigns {
		c := obj(v)
		if text(c["gameId"]) == text(obj(task["game"])["id"]) {
			catalogue[text(c["id"])] = c
		}
	}
	rows := []any{}
	runnable := map[string]bool{}
	blocked, completed, claimPending := 0, 0, 0
	allExpired := true
	for _, v := range refs {
		ref := obj(v)
		campaign := catalogue[text(ref["campaignId"])]
		drops := map[string]M{}
		for _, v := range list(campaign["drops"]) {
			d := obj(v)
			drops[text(d["id"])] = d
		}
		drop := drops[text(ref["dropId"])]
		done := drop != nil && campaign["officialConfirmed"] != false && achieved(drop)
		pending := done && !flag(drop["claimed"])
		row := M{"campaignId": ref["campaignId"], "dropId": ref["dropId"], "name": first(drop["name"], "等待奖励资料"), "campaignName": first(campaign["name"], "等待活动资料"), "completed": done, "claimPending": pending}
		status := "unknown"
		switch {
		case done:
			status = "completed"
			completed++
		case drop == nil:
		case timestamp(campaign["endsAt"]) > 0 && timestamp(campaign["endsAt"]) <= unixNow():
			status = "expired"
		case timestamp(campaign["startsAt"]) > unixNow():
			status = "upcoming"
		case campaign["officialConfirmed"] == false:
			status = "awaiting_verification"
			runnable[text(campaign["id"])] = true
		default:
			status = dependencyState(drop, drops, map[string]bool{})
			if status == "ready" {
				runnable[text(campaign["id"])] = true
			} else if status == "waiting_claim" {
				blocked++
			}
		}
		if pending {
			claimPending++
		}
		row["status"] = status
		rows = append(rows, row)
		if !done && status != "expired" {
			allExpired = false
		}
	}
	status := "unknown"
	switch {
	case len(refs) == 0:
		status = "waiting_targets"
	case completed == len(refs):
		status = "completed"
	case len(runnable) > 0:
		if readable {
			status = "ready"
			for _, row := range rows {
				if obj(row)["status"] == "awaiting_verification" {
					status = "unknown"
				}
			}
		}
	case blocked > 0:
		status = "waiting_claim"
	case allExpired:
		status = "expired"
	}
	ids := []string{}
	for id := range runnable {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	r := []any{}
	for _, id := range ids {
		r = append(r, id)
	}
	return M{"taskId": task["id"], "status": status, "total": len(refs), "completed": completed, "claimPending": claimPending, "blocked": blocked, "runnableCampaignIds": r, "rewards": rows}
}
func mergeProgress(fresh, previous M) M {
	oldDrops := map[string]M{}
	for _, v := range list(previous["drops"]) {
		d := obj(v)
		oldDrops[text(d["id"])] = d
	}
	for _, v := range list(fresh["drops"]) {
		d := obj(v)
		old := oldDrops[text(d["id"])]
		for _, k := range []string{"minutes", "requiredMinutes", "claimed"} {
			if d[k] == nil && old[k] != nil {
				d[k] = old[k]
			}
		}
		if old["minutes"] != nil && d["minutes"] != nil {
			d["minutes"] = max(number(old["minutes"]), number(d["minutes"]))
		}
		if flag(old["claimed"]) {
			d["claimed"] = true
		}
		d["claimable"] = !flag(d["claimed"]) && (flag(d["claimable"]) || flag(old["claimable"]))
		if old["sampledAt"] != nil && number(d["minutes"]) == number(old["minutes"]) && d["claimed"] == old["claimed"] {
			d["sampledAt"] = old["sampledAt"]
		}
	}
	return fresh
}
func cleanCampaign(raw M, sampled string) M {
	drops := []any{}
	for i, v := range list(raw["timeBasedDrops"]) {
		if i >= 100 {
			break
		}
		d := obj(v)
		own := obj(d["self"])
		var minutes, required, claimed any
		if validNumber(own["currentMinutesWatched"]) {
			minutes = own["currentMinutesWatched"]
		}
		if validNumber(d["requiredMinutesWatched"]) {
			required = d["requiredMinutesWatched"]
		}
		if b, ok := own["isClaimed"].(bool); ok {
			claimed = b
		}
		benefits, images, prereqs := []any{}, []any{}, []any{}
		for j, v := range list(d["benefitEdges"]) {
			if j >= 10 {
				break
			}
			b := obj(obj(v)["benefit"])
			name := truncate(text(b["name"]), 200)
			if name != "" {
				benefits = append(benefits, name)
			}
			if u := rewardImageURL(b["imageAssetURL"]); u != nil {
				images = append(images, M{"name": name, "url": u})
			}
		}
		for _, v := range list(d["preconditionDrops"]) {
			prereqs = append(prereqs, text(obj(v)["id"]))
		}
		drops = append(drops, M{"id": text(d["id"]), "name": truncate(text(d["name"]), 200), "benefits": benefits, "images": images, "minutes": minutes, "requiredMinutes": required, "claimed": claimed, "claimable": text(own["dropInstanceID"]) != "" && claimed == false, "prerequisites": prereqs, "sampledAt": sampled})
	}
	game, allow := obj(raw["game"]), obj(raw["allow"])
	channels := []any{}
	if flag(allow["isEnabled"]) {
		for i, v := range list(allow["channels"]) {
			if i >= 100 {
				break
			}
			c := obj(v)
			channels = append(channels, M{"id": text(c["id"]), "login": text(first(c["name"], c["login"]))})
		}
	}
	return M{"id": text(raw["id"]), "name": truncate(text(raw["name"]), 200), "gameId": text(game["id"]), "gameName": truncate(text(game["name"]), 100), "startsAt": raw["startAt"], "endsAt": raw["endAt"], "status": raw["status"], "linked": obj(raw["self"])["isAccountConnected"], "allowChannels": channels, "drops": drops, "sampledAt": sampled}
}
