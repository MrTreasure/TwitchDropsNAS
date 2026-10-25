package main

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const watchIntervalSeconds = 20

type playbackEntry struct {
	url     string
	expires float64
}

var hlsAttribute = regexp.MustCompile(`([A-Z0-9-]+)=("[^"]*"|[^,]*)`)

// Only Twitch playlist metadata may be downloaded. Segment bodies are never read.
func twitchPlaybackDestination(method string, u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "usher.ttvnw.net" {
		login := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/api/channel/hls/"), ".m3u8")
		return method == "GET" && u.Path == "/api/channel/hls/"+login+".m3u8" && channelPattern.MatchString(login)
	}
	cdn := strings.HasSuffix(host, ".ttvnw.net") || strings.HasSuffix(host, ".jtvnw.net")
	if !cdn {
		return false
	}
	if method == "GET" {
		return strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
	}
	return method == "HEAD" && (strings.Contains(u.Path, "/segment/") || strings.HasSuffix(u.Path, ".ts") || strings.HasSuffix(u.Path, ".mp4") || strings.HasSuffix(u.Path, ".m4s"))
}

func hlsURL(base, path, method string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fault("直播播放列表地址无效", "playback_unavailable", 409)
	}
	r, err := url.Parse(path)
	if err != nil {
		return "", fault("直播播放列表地址无效", "playback_unavailable", 409)
	}
	u = u.ResolveReference(r)
	if !twitchPlaybackDestination(method, u) {
		return "", fault("播放列表包含不允许的地址，已停止访问", "invalid_destination", 409)
	}
	return u.String(), nil
}

func lowestPlaylist(base, body string) (string, error) {
	if !strings.HasPrefix(strings.TrimSpace(body), "#EXTM3U") {
		return "", fault("Twitch 未返回有效直播播放列表", "playback_unavailable", 409)
	}
	bandwidth, bestBandwidth := int64(0), int64(1<<62)
	pending, selected := false, ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			pending, bandwidth = true, 1<<61
			for _, m := range hlsAttribute.FindAllStringSubmatch(line, -1) {
				if m[1] == "BANDWIDTH" {
					if n, err := strconv.ParseInt(strings.Trim(m[2], `"`), 10, 64); err == nil && n > 0 {
						bandwidth = n
					}
				}
			}
		} else if pending && line != "" && !strings.HasPrefix(line, "#") {
			candidate, err := hlsURL(base, line, "GET")
			if err != nil {
				return "", err
			}
			if bandwidth < bestBandwidth {
				selected, bestBandwidth = candidate, bandwidth
			}
			pending = false
		}
	}
	if selected == "" {
		return "", fault("直播没有可用的清晰度列表", "playback_unavailable", 409)
	}
	return selected, nil
}

func playlistSegments(base, body string) ([]string, error) {
	if !strings.HasPrefix(strings.TrimSpace(body), "#EXTM3U") {
		return nil, fault("直播分片列表无效", "playback_unavailable", 409)
	}
	segments, seen := []string{}, map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := hlsURL(base, line, "HEAD")
		if err != nil {
			return nil, err
		}
		if !seen[u] {
			segments = append(segments, u)
			seen[u] = true
		}
		if len(segments) > 32 {
			return nil, fault("直播分片列表过长，已停止本轮检查", "response_limit", 409)
		}
	}
	if len(segments) == 0 {
		return nil, fault("直播暂未返回分片", "playback_unavailable", 409)
	}
	return segments, nil
}

func (t *Twitch) playbackURL(ctx context.Context, account, channel M) (string, error) {
	key := text(account["user_id"]) + ":" + text(channel["id"]) + ":" + text(obj(channel["stream"])["id"])
	t.playbackMu.Lock()
	entry := t.playback[key]
	t.playbackMu.Unlock()
	if entry.url != "" && entry.expires > unixNow() {
		return entry.url, nil
	}
	r, err := t.gql(ctx, account, "", M{"isLive": true, "isVod": false, "login": channel["login"], "platform": "web", "playerType": "site", "vodID": ""}, "Playback")
	if err != nil {
		return "", err
	}
	token := obj(r["streamPlaybackAccessToken"])
	if text(token["value"]) == "" || text(token["signature"]) == "" {
		return "", fault("Twitch 未提供此直播的播放访问权限", "playback_unavailable", 409)
	}
	master := "https://usher.ttvnw.net/api/channel/hls/" + text(channel["login"]) + ".m3u8?" + url.Values{"sig": {text(token["signature"])}, "token": {text(token["value"])}}.Encode()
	status, result, err := t.net.request(ctx, "GET", master, nil, nil, nil, 131072)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fault("Twitch 未接受直播播放列表请求", "playback_unavailable", 409)
	}
	selected, err := lowestPlaylist(master, text(result["text"]))
	if err != nil {
		return "", err
	}
	expires := unixNow() + 900
	var data M
	if json.Unmarshal([]byte(text(token["value"])), &data) == nil && number(data["expires"]) > 0 {
		expires = min(expires, number(data["expires"])-60)
	}
	t.playbackMu.Lock()
	// One active player per service; never retain old-account playback credentials.
	t.playback = map[string]playbackEntry{key: {url: selected, expires: expires}}
	t.playbackMu.Unlock()
	return selected, nil
}

// Matches the maintained upstream's HLS/HEAD path. No video or audio segment GETs.
func (t *Twitch) watch(ctx context.Context, account, channel M) error {
	if text(obj(channel["stream"])["id"]) == "" {
		return fault("当前频道已离线", "channel_offline", 409)
	}
	playlist, err := t.playbackURL(ctx, account, channel)
	if err != nil {
		return err
	}
	status, result, err := t.net.request(ctx, "GET", playlist, nil, nil, nil, 131072)
	if err != nil {
		return err
	}
	if status != 200 {
		t.playbackMu.Lock()
		t.playback = nil
		t.playbackMu.Unlock()
		return fault("直播分片列表访问失败，等待重新核对", "playback_unavailable", 409)
	}
	segments, err := playlistSegments(playlist, text(result["text"]))
	if err != nil {
		return err
	}
	for _, segment := range segments {
		status, _, err := t.net.request(ctx, "HEAD", segment, nil, nil, nil, 1)
		if err != nil {
			return err
		}
		if status != 200 {
			return fault("直播分片HEAD检查未通过", "playback_unavailable", 409)
		}
	}
	return nil
}
