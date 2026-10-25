package main

import (
	"context"
	"testing"
	"time"
)

func TestIdleScheduleAvoidsNetworkAndPreservesProgress(t *testing.T) {
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	task := M{"id": "wt", "enabled": true, "game": M{"id": "66366", "name": "War Thunder"}, "goal": defaultGoal()}
	store.PutMany(M{"running": true, "tasks": []any{task}, "campaigns": []any{}, "progress": M{"status": "unknown", "sampledAt": "2026-10-20T00:00:00Z"}})
	s := &Service{store: store, account: M{"login": "synthetic-test-only"}, streamSchedules: []any{M{"gameId": "66366", "gameName": "War Thunder", "sessions": []any{M{"startsAt": time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)}}}}}
	// No transport is installed: neither path may touch a network dependency.
	if err := s.engineStep(context.Background()); err != nil || s.state != "scheduled_wait" {
		t.Fatalf("idle watcher reached transport: %v", err)
	}
	result, err := s.scan(context.Background(), false)
	if err != nil || flag(result["checked"]) || result["reason"] != "waiting_for_query_window" {
		t.Fatalf("idle metadata query was not skipped: %v %v", result, err)
	}
	if obj(store.Get("progress", nil))["sampledAt"] != "2026-10-20T00:00:00Z" {
		t.Fatal("a skipped query changed official provenance")
	}
	if store.Get("automationLastStatus", nil) != "skipped" {
		t.Fatal("a skipped query was reported as a real sync")
	}
}

func TestPublishedStreamBoundaries(t *testing.T) {
	task := M{"id": "wt", "enabled": true, "game": M{"id": "66366", "name": "War Thunder"}}
	published := []any{M{"gameId": "66366", "gameName": "War Thunder", "sessions": []any{M{"startsAt": "2026-10-25T13:50:00Z"}, M{"startsAt": "2026-10-26T17:00:00Z"}}}}
	for _, c := range []struct {
		at, state string
		active    bool
		next      string
	}{
		{"2026-10-25T21:44:59+08:00", "scheduled", false, "2026-10-25T21:45:00+08:00"},
		{"2026-10-25T21:45:00+08:00", "preflight", true, ""},
		{"2026-10-25T21:50:00+08:00", "active", true, ""},
		{"2026-10-25T22:50:00+08:00", "scheduled", false, "2026-10-27T00:55:00+08:00"},
		{"2026-10-27T00:55:00+08:00", "preflight", true, ""},
		{"2026-10-27T02:00:00+08:00", "schedule_finished", false, ""},
	} {
		t.Run(c.at, func(t *testing.T) {
			at, _ := time.Parse(time.RFC3339, c.at)
			p := taskQueryPlan(task, published, float64(at.Unix()), false)
			if p["state"] != c.state || flag(p["active"]) != c.active {
				t.Fatalf("unexpected window state: %v", p)
			}
			if c.next != "" {
				next, _ := time.Parse(time.RFC3339, c.next)
				if number(p["nextWakeAt"]) != float64(next.Unix()) {
					t.Fatalf("unexpected query time: %v", p)
				}
			}
		})
	}
	at, _ := time.Parse(time.RFC3339, "2026-10-27T06:00:00+08:00")
	p := taskQueryPlan(task, published, float64(at.Unix()), true)
	if !flag(p["active"]) || p["state"] != "watching" {
		t.Fatal("published start time must not impose a false live-stream end")
	}
}

func TestBundledWTCSStreamTimes(t *testing.T) {
	t.Setenv("TDM_STREAM_SCHEDULES_FILE", "")
	sources, err := loadStreamSchedules()
	if err != nil || len(sources) != 1 || len(list(obj(sources[0])["sessions"])) != 18 {
		t.Fatalf("missing verified stream catalogue: %v", err)
	}
	sessions := list(obj(sources[0])["sessions"])
	if obj(sessions[0])["startsAt"] != "2026-10-23T14:50:00Z" || obj(sessions[17])["startsAt"] != "2026-11-11T17:00:00Z" {
		t.Fatal("official GMT dates changed")
	}
}

func TestUnscheduledGamesAndManualTimes(t *testing.T) {
	task := M{"enabled": true, "game": M{"name": "Other Game"}}
	if p := taskQueryPlan(task, nil, 0, false); !flag(p["active"]) || flag(p["known"]) {
		t.Fatal("unknown schedules must retain low-frequency discovery")
	}
	_, err := validateQuerySchedule(M{"mode": "custom", "sessions": []any{M{"startsAt": "2026-10-25T21:50:00"}}}, nil, M{})
	if err == nil {
		t.Fatal("timezone-free stream times must be rejected")
	}
	plan, err := validateQuerySchedule(M{"mode": "custom", "sessions": []any{M{"startsAt": "2026-10-25T21:50:00+08:00"}}}, nil, M{})
	if err != nil || obj(list(plan["sessions"])[0])["startsAt"] != "2026-10-25T13:50:00Z" {
		t.Fatal("manual Beijing time must preserve its exact UTC instant")
	}
	task["enabled"] = false
	if flag(taskQueryPlan(task, nil, 0, true)["active"]) {
		t.Fatal("disabled tasks must not query")
	}
}
