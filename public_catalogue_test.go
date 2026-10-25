package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Opt-in compatibility check against a privately saved anonymous provider
// response. No live user snapshot or provider response belongs in the repo.
func TestPublicCatalogueProviderFixture(t *testing.T) {
	path := os.Getenv("TDM_TEST_PUBLIC_CATALOGUE")
	if path == "" {
		t.Skip("no anonymous provider fixture supplied")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload M
	if json.Unmarshal(raw, &payload) != nil {
		t.Fatal("invalid provider JSON")
	}
	items, err := parsePublicCatalogue(payload, timestamp(payload["lastUpdatedAt"]))
	if err != nil {
		t.Fatal(err)
	}
	games := map[string]bool{}
	for _, v := range items {
		games[text(obj(v)["gameId"])] = true
	}
	if len(items) < 2 || len(games) < 2 {
		t.Fatal("provider response did not produce multiple games/campaigns")
	}
	t.Logf("provider fixture: %d campaigns across %d games", len(items), len(games))
}

func TestPublicCampaignCanBeSelectedWithoutChangingTasks(t *testing.T) {
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	items, err := parsePublicCatalogue(publicTestPayload(), unixNow())
	if err != nil {
		t.Fatal(err)
	}
	store.PutMany(M{"publicCatalogueCache": M{"items": items}, "tasks": []any{}, "running": false})
	n := newTransport(store, &Proxy{})
	defer n.close()
	s := &Service{store: store, net: n, account: M{"user_id": "synthetic-account"}}
	w := &Web{s: s}
	r := httptest.NewRequest("POST", "/tasks/preview", strings.NewReader(string(bytesJSON(M{"campaignId": "candidate"}))))
	out := httptest.NewRecorder()
	w.handler(true).ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("public campaign selection failed", out.Code, out.Body.String())
	}
	var snapshot M
	if json.Unmarshal(out.Body.Bytes(), &snapshot) != nil || len(list(snapshot["campaignDirectory"])) != 1 || len(list(snapshot["campaigns"])) != 1 || len(list(snapshot["tasks"])) != 0 || flag(snapshot["running"]) {
		t.Fatal("draft selection created or started a task")
	}
	if obj(list(snapshot["campaigns"])[0])["officialConfirmed"] != false {
		t.Fatal("candidate was presented as official account data")
	}
}

func publicTestPayload() M {
	drop := M{"id": "reward", "requiredMinutesWatched": 1, "self": M{"currentMinutesWatched": 999, "isClaimed": true, "dropInstanceID": "untrusted"}}
	campaign := M{"id": "candidate", "name": "Synthetic campaign", "startAt": iso(unixNow() - 60), "endAt": iso(unixNow() + 3600), "status": "ACTIVE", "game": M{"id": "game", "displayName": "Synthetic game", "slug": "synthetic-game"}, "self": M{"isAccountConnected": true}, "allow": M{"isEnabled": true, "channels": []any{M{"id": "channel", "name": "synthetic"}}}, "timeBasedDrops": []any{drop}}
	return M{"lastUpdatedAt": now(), "refreshIntervalSeconds": 60, "data": []any{M{"rewards": []any{campaign}}}}
}

func TestPublicCatalogueCannotSupplyAccountStateOrCompletion(t *testing.T) {
	items, err := parsePublicCatalogue(publicTestPayload(), unixNow())
	if err != nil || len(items) != 1 {
		t.Fatal("valid catalogue rejected", err)
	}
	c := obj(items[0])
	d := obj(list(c["drops"])[0])
	if c["linked"] != nil || c["officialConfirmed"] != false || d["minutes"] != nil || d["requiredMinutes"] != nil || d["claimed"] != nil || flag(d["claimable"]) {
		t.Fatal("public account/progress fields were trusted")
	}
	// Even corrupted/stale provisional rows cannot cause completion.
	d["minutes"], d["requiredMinutes"], d["claimed"] = 999, 1, true
	task := M{"id": "task", "game": M{"id": "game"}, "goal": M{"mode": "selected", "rewards": []any{M{"campaignId": "candidate", "dropId": "reward"}}}}
	state := evaluate(task, items, true)
	if number(state["completed"]) != 0 || state["status"] == "completed" {
		t.Fatal("public catalogue completed a goal")
	}
	payload := publicTestPayload()
	payload["lastUpdatedAt"] = iso(unixNow() - 7200)
	if _, err := parsePublicCatalogue(payload, unixNow()); err == nil {
		t.Fatal("stale catalogue accepted")
	}
}

func TestAnonymousCatalogueFetchIsCachedAndCannotPauseTwitch(t *testing.T) {
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	store.Put("settings", M{"networkMode": "direct"})
	n := newTransport(store, &Proxy{})
	defer n.close()
	s := &Service{store: store, net: n}
	requests := 0
	n.client.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.String() != publicCatalogueURL || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Client-ID") != "" {
			t.Fatal("private data or unexpected destination in catalogue request")
		}
		payload, _ := json.Marshal(publicTestPayload())
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
	})
	for i := 0; i < 2; i++ {
		if err := s.refreshPublicCatalogue(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatal("catalogue cache did not bound traffic")
	}
	if _, _, err := n.request(context.Background(), "GET", publicCatalogueURL, M{"Authorization": "synthetic-secret"}, nil, nil, 1024); err == nil {
		t.Fatal("credential forwarding accepted")
	}
	n.client.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	if _, _, err := n.request(context.Background(), "GET", publicCatalogueURL, nil, nil, nil, 1024); err == nil || n.blocked() {
		t.Fatal("public failure paused Twitch playback")
	}
}

func TestPublicChannelHintRequiresOfficialEligibility(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unverified", true: "official-channel"}[confirmed], func(t *testing.T) {
			store, err := openStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.db.Close()
			items, err := parsePublicCatalogue(publicTestPayload(), unixNow())
			if err != nil {
				t.Fatal(err)
			}
			task := M{"id": "task", "enabled": true, "priority": 50, "watchOnly": false, "autoChannels": false, "channels": []any{}, "querySchedule": M{"mode": "always"}, "game": M{"id": "game", "name": "Synthetic game"}, "goal": M{"mode": "selected", "rewards": []any{M{"campaignId": "candidate", "dropId": "reward"}}}}
			store.PutMany(M{"settings": M{"networkMode": "direct"}, "running": true, "tasks": []any{task}, "campaigns": items, "progress": M{"status": "available"}})
			n := newTransport(store, &Proxy{})
			defer n.close()
			s := &Service{store: store, net: n, account: M{"user_id": "synthetic-account", "client_id": deviceClient, "access_token": "synthetic-test-only"}, twitch: &Twitch{net: n}}
			availableChecks := 0
			n.client.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
				var input M
				_ = json.NewDecoder(r.Body).Decode(&input)
				data := M{}
				if input["operationName"] == "DropsHighlightService_AvailableDrops" {
					availableChecks++
					campaigns := []any{}
					if confirmed {
						campaigns = append(campaigns, M{"id": "candidate", "name": "Official campaign", "game": M{"id": "game", "name": "Synthetic game"}, "endAt": iso(unixNow() + 3600), "timeBasedDrops": []any{M{"id": "reward", "startAt": iso(unixNow() - 60), "requiredMinutesWatched": 30}}})
					}
					data = M{"channel": M{"viewerDropCampaigns": campaigns}}
				} else {
					data = M{"channel0": M{"id": "channel", "login": "synthetic", "stream": M{"id": "stream", "game": M{"id": "game", "name": "Synthetic game"}}}}
				}
				body, _ := json.Marshal(M{"data": data})
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			if err := s.selectChannel(context.Background()); err != nil {
				t.Fatal(err)
			}
			if availableChecks != 1 || (s.current != nil) != confirmed {
				t.Fatal("public allowlist bypassed official eligibility")
			}
			if confirmed {
				c := obj(list(store.Get("campaigns", nil))[0])
				d := obj(list(c["drops"])[0])
				if c["officialConfirmed"] != true || number(d["requiredMinutes"]) != 30 || c["linked"] != nil || d["minutes"] != nil {
					t.Fatal("official requirements/account state not preserved")
				}
			}
		})
	}
}
