package main

import (
	"encoding/json"
	"os"
	"sort"
	"time"
)

const streamLeadSeconds = 300

// This is a local late-start policy, not an asserted official stream end time.
const streamLateSeconds = 3600

func loadStreamSchedules() ([]any, error) {
	path := os.Getenv("TDM_STREAM_SCHEDULES_FILE")
	var raw []byte
	var err error
	if path == "" {
		raw, err = bundled.ReadFile("catalogue/stream-schedules.json")
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fail("直播场次资料无法读取")
	}
	var schedules []any
	if json.Unmarshal(raw, &schedules) != nil || len(schedules) > 100 {
		return nil, fail("直播场次资料无效")
	}
	for _, v := range schedules {
		s := obj(v)
		if text(s["gameName"]) == "" || len(list(s["sessions"])) > 100 {
			return nil, fail("直播场次资料无效")
		}
		for _, entry := range list(s["sessions"]) {
			if _, e := time.Parse(time.RFC3339, text(obj(entry)["startsAt"])); e != nil {
				return nil, fail("直播场次必须包含明确时区")
			}
		}
	}
	return schedules, nil
}

func validateQuerySchedule(raw any, old, game M) (M, error) {
	if raw == nil {
		if text(obj(old["game"])["name"]) == text(game["name"]) && old["querySchedule"] != nil {
			return obj(old["querySchedule"]), nil
		}
		return M{"mode": "auto", "sessions": []any{}}, nil
	}
	input, ok := raw.(map[string]any)
	if !ok {
		return nil, fail("直播查询计划格式无效")
	}
	mode := text(input["mode"])
	if mode != "auto" && mode != "always" && mode != "custom" {
		return nil, fail("直播查询计划模式无效")
	}
	if mode != "custom" {
		return M{"mode": mode, "sessions": []any{}}, nil
	}
	entries, ok := input["sessions"].([]any)
	if !ok || len(entries) < 1 || len(entries) > 100 {
		return nil, fail("请设置 1–100 个直播开播时间")
	}
	sessions := []any{}
	seen := map[int64]bool{}
	for _, entry := range entries {
		t, err := time.Parse(time.RFC3339, text(obj(entry)["startsAt"]))
		if err != nil || t.Year() < 2000 || t.Year() > 2200 {
			return nil, fail("开播时间需为含时区的有效日期时间")
		}
		if !seen[t.Unix()] {
			sessions = append(sessions, M{"startsAt": t.UTC().Format(time.RFC3339), "label": "自定义直播"})
			seen[t.Unix()] = true
		}
	}
	return M{"mode": mode, "sessions": sessions}, nil
}

// Pure time policy shared by the watcher, the metadata executor and the UI.
// Campaign startsAt/endsAt are never used as advertised stream times.
func taskQueryPlan(task M, published []any, at float64, watching bool) M {
	config := obj(task["querySchedule"])
	mode := text(config["mode"])
	if mode == "" {
		mode = "auto"
	}
	plan := M{"taskId": task["id"], "mode": mode, "active": false, "known": false, "state": "disabled", "nextWakeAt": nil, "startsAt": nil, "boundaryAt": nil, "leadSeconds": streamLeadSeconds, "lateStartSeconds": streamLateSeconds}
	if !flag(task["enabled"]) {
		return plan
	}
	sessions := []any{}
	if mode == "custom" {
		sessions = append(sessions, list(config["sessions"])...)
		plan["known"] = true
	}
	if mode == "auto" {
		game := obj(task["game"])
		for _, v := range published {
			source := obj(v)
			if text(game["id"]) != "" && text(source["gameId"]) == text(game["id"]) || normalized(source["gameName"]) == normalized(game["name"]) {
				plan["known"] = true
				plan["sourceUrl"], plan["verifiedAt"] = source["sourceUrl"], source["verifiedAt"]
				sessions = append(sessions, list(source["sessions"])...)
			}
		}
	}
	if mode == "always" || !flag(plan["known"]) {
		plan["active"], plan["state"] = true, "fallback"
		return plan
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return timestamp(obj(sessions[i])["startsAt"]) < timestamp(obj(sessions[j])["startsAt"])
	})
	for _, v := range sessions {
		session := obj(v)
		start := timestamp(session["startsAt"])
		if start <= 0 || at >= start+streamLateSeconds {
			continue
		}
		plan["startsAt"], plan["label"], plan["kind"] = session["startsAt"], session["label"], session["kind"]
		if at < start-streamLeadSeconds {
			plan["nextWakeAt"], plan["state"] = start-streamLeadSeconds, "scheduled"
		} else {
			plan["active"], plan["state"], plan["boundaryAt"] = true, "active", start+streamLateSeconds
			if at < start {
				plan["state"], plan["boundaryAt"] = "preflight", start
			}
		}
		break
	}
	if plan["startsAt"] == nil {
		plan["state"] = "schedule_finished"
	}
	// Once a real stream is selected, a guessed end must never interrupt it.
	if watching {
		plan["active"], plan["state"] = true, "watching"
	}
	return plan
}

func (s *Service) queryPlanLocked(at float64) M {
	result := M{"active": false, "nextWakeAt": nil, "state": "idle", "leadSeconds": streamLeadSeconds, "tasks": []any{}}
	completed := map[string]bool{}
	for _, v := range s.goalStatesLocked() {
		if obj(v)["status"] == "completed" {
			completed[text(obj(v)["taskId"])] = true
		}
	}
	plans := []any{}
	for _, v := range list(s.store.Get("tasks", []any{})) {
		t := obj(v)
		p := taskQueryPlan(t, s.streamSchedules, at, text(s.current["taskId"]) == text(t["id"]) && s.current != nil)
		if completed[text(t["id"])] {
			p["active"], p["state"], p["nextWakeAt"] = false, "completed", nil
		}
		plans = append(plans, p)
		if flag(p["active"]) {
			result["active"] = true
		}
		if next := number(p["nextWakeAt"]); next > at && (result["nextWakeAt"] == nil || next < number(result["nextWakeAt"])) {
			result["nextWakeAt"] = next
		}
	}
	result["tasks"] = plans
	if flag(result["active"]) {
		result["state"] = "querying"
	} else if result["nextWakeAt"] != nil {
		result["state"] = "scheduled"
	}
	if !flag(s.store.Get("running", false)) {
		result["active"], result["state"], result["nextWakeAt"] = false, "paused", nil
	}
	return result
}

func (s *Service) nextMetadataCheckLocked(at float64) any {
	plan := s.queryPlanLocked(at)
	if !flag(plan["active"]) {
		return plan["nextWakeAt"]
	}
	next := at + s.checkDelay()
	for _, v := range list(plan["tasks"]) {
		if boundary := number(obj(v)["boundaryAt"]); boundary > at && boundary < next {
			next = boundary
		}
	}
	return next
}

func scheduledReason(plan M) string {
	if next := number(plan["nextWakeAt"]); next > 0 {
		return "等待已公布的直播场次；将于 " + time.Unix(int64(next), 0).In(time.FixedZone("UTC+8", 8*3600)).Format("01-02 15:04") + "（北京时间）开始查询，即开播前 5 分钟"
	}
	return "已公布场次的开播等待期已结束；可补充新场次或切换不限时间查询"
}
