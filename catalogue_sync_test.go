package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A manual sync must not fan out into campaign details, and explicit proxy
// mode must never probe or fall back to the direct transport.
func TestCatalogueSyncUsesProxyWithoutFetchingDetails(t *testing.T) {
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	store.PutMany(M{"settings": M{"networkMode": "proxy", "checkMinSeconds": 600, "checkMaxSeconds": 1200}, "tasks": []any{M{"id": "task", "enabled": true, "game": M{"id": "game", "name": "Game"}, "goal": defaultGoal()}}, "running": false, "campaigns": []any{}})
	n := newTransport(store, &Proxy{URL: "http://127.0.0.1:1"})
	defer n.close()
	n.client.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) { t.Fatal("sync used direct transport"); return nil, nil })
	calls := []string{}
	n.proxyClient.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
		var body M
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		op := text(body["operationName"])
		calls = append(calls, op)
		data := M{}
		switch op {
		case "Inventory":
			data = M{"currentUser": M{"inventory": M{"dropCampaignsInProgress": []any{}}}}
		case "ViewerDropsDashboard":
			data = M{"currentUser": M{"dropCampaigns": []any{M{"id": "campaign", "status": "ACTIVE", "game": M{"id": "game", "name": "Game"}}}}}
		default:
			t.Fatalf("unexpected request: %s", op)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(bytesJSON(M{"data": data}))))}, nil
	})
	s := &Service{store: store, net: n, twitch: &Twitch{net: n}, account: M{"user_id": "user", "client_id": webClient, "access_token": "test-only", "validatedAt": now(), "expiresAt": unixNow() + 7200}, ctx: context.Background()}
	if _, err := s.scan(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || len(list(obj(store.Get("catalogueCache", nil))["items"])) != 1 {
		t.Fatalf("unexpected sync result: %v", calls)
	}
}
