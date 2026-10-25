package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type playbackRoundTrip func(*http.Request) (*http.Response, error)

type forbiddenMediaBody struct{ t *testing.T }

func (b forbiddenMediaBody) Read([]byte) (int, error) {
	b.t.Fatal("attempted to read a segment response body")
	return 0, io.EOF
}
func (b forbiddenMediaBody) Close() error { return nil }

func (f playbackRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlaybackOnlyFetchesManifestsAndHeadsSegments(t *testing.T) {
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	store.Put("settings", M{"networkMode": "direct"})
	n := newTransport(store, &Proxy{})
	defer n.close()
	var tokenRequests, masters, manifests, heads int
	n.client.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, status := "", 200
		if r.URL.Hostname() != "gql.twitch.tv" && r.Header.Get("Authorization") != "" {
			t.Fatal("OAuth leaked to CDN")
		}
		switch r.URL.Hostname() {
		case "gql.twitch.tv":
			tokenRequests++
			body = `{"data":{"streamPlaybackAccessToken":{"value":"{\"expires\":4102444800}","signature":"synthetic-signature"}}}`
		case "usher.ttvnw.net":
			masters++
			body = "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=900000\nhttps://video-weaver.test.hls.ttvnw.net/high.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=64000\nhttps://video-weaver.test.hls.ttvnw.net/low.m3u8\n"
		case "video-weaver.test.hls.ttvnw.net":
			if r.URL.Path != "/low.m3u8" || r.Method != "GET" {
				t.Fatalf("wrong playlist: %s", r.URL.Path)
			}
			manifests++
			body = "#EXTM3U\n#EXTINF:2,\nhttps://video-edge.test.hls.ttvnw.net/segment/1.ts\n#EXTINF:2,\nhttps://video-edge.test.hls.ttvnw.net/segment/2.ts\n"
		case "video-edge.test.hls.ttvnw.net":
			if r.Method != "HEAD" {
				t.Fatal("attempted a media download")
			}
			heads++
		default:
			t.Fatalf("unexpected destination %s", r.URL.Hostname())
		}
		var responseBody io.ReadCloser = io.NopCloser(strings.NewReader(body))
		if r.Method == "HEAD" {
			responseBody = forbiddenMediaBody{t}
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: responseBody}, nil
	})
	tw := &Twitch{net: n}
	account := M{"client_id": webClient, "access_token": "synthetic-test-only", "user_id": "test-user"}
	channel := M{"id": "test-channel", "login": "example", "stream": M{"id": "test-stream"}}
	for i := 0; i < 2; i++ {
		if err := tw.watch(context.Background(), account, channel); err != nil {
			t.Fatal(err)
		}
	}
	if tokenRequests != 1 || masters != 1 || manifests != 2 || heads != 4 {
		t.Fatalf("cache or playback path failed: %d %d %d %d", tokenRequests, masters, manifests, heads)
	}
	if _, _, err := n.request(context.Background(), "GET", "https://video-edge.test.hls.ttvnw.net/segment/1.ts", nil, nil, nil, 100); err == nil {
		t.Fatal("segment GET was allowed")
	}
}

func TestPlaylistRejectsNonTwitchAndUnboundedSegments(t *testing.T) {
	for _, s := range []string{"http://video-edge.test.ttvnw.net/segment/a.ts", "https://127.0.0.1/a.ts", "https://video-edge.test.ttvnw.net.attacker.invalid/a.ts", "https://user@video-edge.test.ttvnw.net/a.ts", "https://video-edge.test.ttvnw.net:444/a.ts"} {
		u, _ := url.Parse(s)
		if twitchPlaybackDestination("HEAD", u) {
			t.Fatalf("unsafe target accepted: %s", s)
		}
	}
	if _, err := playlistSegments("https://video-weaver.test.ttvnw.net/a.m3u8", "#EXTM3U\nhttps://example.invalid/segment/a.ts"); err == nil {
		t.Fatal("foreign segment accepted")
	}
	lines := "#EXTM3U\n"
	for i := 0; i < 33; i++ {
		lines += "https://video-edge.test.ttvnw.net/segment/" + text(i) + ".ts\n"
	}
	if _, err := playlistSegments("https://video-weaver.test.ttvnw.net/a.m3u8", lines); err == nil {
		t.Fatal("unbounded segment list accepted")
	}
}

func TestEmptyDropSessionIsNotOfficialProgress(t *testing.T) {
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	store.Put("settings", M{"networkMode": "direct"})
	n := newTransport(store, &Proxy{})
	defer n.close()
	n.client.Transport = playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":{"currentUser":{"dropCurrentSession":{"dropID":"","currentMinutesWatched":0,"channel":null}}}}`))}, nil
	})
	result, err := (&Twitch{net: n}).current(context.Background(), M{"client_id": webClient, "access_token": "synthetic-test-only"}, M{"id": "example"})
	if err != nil || len(result) != 0 {
		t.Fatalf("empty target became a real progress result: %v %v", result, err)
	}
}
