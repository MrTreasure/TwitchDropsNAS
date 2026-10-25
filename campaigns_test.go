package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCampaignDirectoryAndDraftDoNotChangeLiveTask(t *testing.T) {
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	goal := M{"mode": "selected", "rewards": []any{M{"campaignId": "known", "dropId": "reward"}}}
	tasks := []any{M{"id": "existing", "enabled": true, "goal": goal}}
	campaign := M{"id": "known", "name": "Synthetic campaign", "gameId": "game", "gameName": "Synthetic game", "sampledAt": now(), "drops": []any{M{"id": "reward"}}}
	store.PutMany(M{"tasks": tasks, "running": true, "campaigns": []any{campaign}, "catalogueCache": M{"accountId": "other-account", "items": []any{M{"id": "foreign", "game": M{"id": "foreign-game", "name": "Foreign game"}}}}})
	s := &Service{store: store, account: M{"user_id": "current-account"}, current: M{"taskId": "existing"}}
	beforeTasks, beforeCurrent := clone(store.Get("tasks", nil)), clone(s.current)
	rows := s.campaignDirectoryLocked()
	if len(rows) != 1 || obj(rows[0])["id"] != "known" {
		t.Fatal("directory leaked another account's catalogue")
	}
	// No transport: cached previews and unknown IDs must never contact Twitch.
	if err := s.previewCampaign(context.Background(), M{"campaignId": "known"}); err != nil {
		t.Fatal(err)
	}
	if err := s.previewCampaign(context.Background(), M{"campaignId": "foreign"}); errorCode(err) != "unknown_campaign" {
		t.Fatal("foreign ID accepted")
	}
	if !reflect.DeepEqual(beforeTasks, store.Get("tasks", nil)) || !reflect.DeepEqual(beforeCurrent, clone(s.current)) || !flag(store.Get("running", nil)) {
		t.Fatal("browsing changed a live task")
	}
	s.account = nil
	if len(s.campaignDirectoryLocked()) != 0 {
		t.Fatal("disconnected account has a directory")
	}
}

func TestCampaignPreviewFetchesOneDetailAndRejectsAccountRace(t *testing.T) {
	for _, accountChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "detail-and-cache", true: "account-change"}[accountChange], func(t *testing.T) {
			store, err := openStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.db.Close()
			store.PutMany(M{"settings": M{"networkMode": "direct"}, "tasks": []any{}, "running": false, "progress": M{"status": "unknown"}, "catalogueCache": M{"accountId": "current-account", "items": []any{M{"id": "new-campaign", "game": M{"id": "game", "name": "Synthetic game"}}}}})
			n := newTransport(store, &Proxy{})
			defer n.close()
			s := &Service{store: store, account: M{"user_id": "current-account", "login": "synthetic", "client_id": webClient, "access_token": "synthetic-test-only"}, twitch: &Twitch{net: n}}
			requests := 0
			n.client.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Hostname() != "gql.twitch.tv" || r.Method != "POST" {
					t.Fatal("unexpected request")
				}
				if accountChange {
					s.mu.Lock()
					s.epoch++
					s.account = M{"user_id": "replacement"}
					s.mu.Unlock()
				}
				raw := M{"id": "new-campaign", "name": "Synthetic", "game": M{"id": "game", "name": "Synthetic game"}, "timeBasedDrops": []any{M{"id": "new-reward", "requiredMinutesWatched": 30}}}
				payload, _ := json.Marshal(M{"data": M{"user": M{"dropCampaign": raw}}})
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
			})
			err = s.previewCampaign(context.Background(), M{"campaignId": "new-campaign"})
			if accountChange {
				if errorCode(err) != "state_changed" || len(list(store.Get("campaigns", nil))) != 0 {
					t.Fatal("stale account details stored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.previewCampaign(context.Background(), M{"campaignId": "new-campaign"}); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || flag(store.Get("running", nil)) || len(list(store.Get("tasks", nil))) != 0 || obj(store.Get("progress", nil))["status"] != "unknown" {
				t.Fatal("preview created work or fabricated inventory readability")
			}
			campaigns := list(store.Get("campaigns", nil))
			if _, err := validateGoal(M{"mode": "selected", "rewards": []any{M{"campaignId": "new-campaign", "dropId": "new-reward"}}}, nil, M{"id": "different-game"}, campaigns); err == nil {
				t.Fatal("cross-game reward selection accepted")
			}
		})
	}
}
