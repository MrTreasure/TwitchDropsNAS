package main

import (
	"context"
	"strings"
	"sync"
)

const webClient = "kimne78kx3ncx6brgo4mv6wki5h1ko"
const deviceClient = "ue6666qo983tsx6so1t0vnawi233wa"

// Public interoperability identifiers from the upstreams credited in NOTICE.md.
var operations = map[string][2]string{
	"Inventory": {"Inventory", "8337eb8541b314040b0edde0c09c5c7a2783ba1960aa9edfbf3bac16d0fec404"},
	"Campaigns": {"ViewerDropsDashboard", "c16bb890cc8ce7647a96ee69cd313d423a378a3dedadf630a1017cde18975feb"},
	"Details":   {"DropCampaignDetails", "039277bf98f3130929262cc7c6efd9c141ca3749cb6dca442fc8ead9a53f77c1"},
	"Available": {"DropsHighlightService_AvailableDrops", "782dad0f032942260171d2d80a654f88bdd0c5a9dddc392e9bc92218a0f42d20"},
	"Current":   {"DropCurrentSessionContext", "4d06b702d25d652afb9ef835d2a550031f1cf762b193523a92166f40ea3d142b"},
	"Playback":  {"PlaybackAccessToken", "ed230aa1e33e07eebb8928504583da78a5173989fadfb1ac94be06a04f3cdbe9"},
}

type Twitch struct {
	net        *Transport
	playbackMu sync.Mutex
	playback   map[string]playbackEntry
}

func (t *Twitch) validate(ctx context.Context, token string) (M, error) {
	status, r, e := t.net.request(ctx, "GET", "https://id.twitch.tv/oauth2/validate", M{"Authorization": "OAuth " + token}, nil, nil, 8192)
	if e != nil {
		return nil, e
	}
	if status != 200 || text(r["user_id"]) == "" || text(r["client_id"]) == "" {
		return nil, fault("Twitch 登录已失效，请重新授权", "login_required", 401)
	}
	return r, nil
}
func (t *Twitch) gql(ctx context.Context, account M, query string, variables M, operation string) (M, error) {
	client := webClient
	if account != nil {
		client = text(account["client_id"])
	}
	headers := M{"Client-ID": client, "Origin": "https://www.twitch.tv", "Referer": "https://www.twitch.tv/"}
	if account != nil {
		headers["Authorization"] = "OAuth " + text(account["access_token"])
		if client == deviceClient {
			headers["Origin"] = "https://android.tv.twitch.tv"
			headers["Referer"] = "https://android.tv.twitch.tv/"
			headers["User-Agent"] = "Mozilla/5.0 (Linux; Android 7.1; Smart Box C1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"
		}
	}
	if variables == nil {
		variables = M{}
	}
	payload := M{"query": query, "variables": variables}
	if operation != "" {
		op := operations[operation]
		payload = M{"operationName": op[0], "variables": variables, "extensions": M{"persistedQuery": M{"version": 1, "sha256Hash": op[1]}}}
	}
	status, r, e := t.net.request(ctx, "POST", "https://gql.twitch.tv/gql", headers, nil, payload, 2097152)
	if e != nil {
		return nil, e
	}
	if status == 401 || status == 403 {
		return nil, fault("Twitch 拒绝此接口访问，请在官方库存核对进度", "capability_unavailable", 409)
	}
	if flag(r["_invalidShape"]) {
		return nil, fault("Twitch 响应格式已变化，保留上次结果", "capability_unavailable", 409)
	}
	if status != 200 || len(list(r["errors"])) > 0 {
		msg := strings.ToLower(string(bytesJSON(r["errors"])))
		if strings.Contains(msg, "integrity") {
			return nil, fault("Twitch 要求完整性验证，此接口已停止自动请求；请在 Twitch 官方页面查看进度", "integrity_required", 409)
		}
		if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authenticated") {
			return nil, fault("Twitch 未开放此授权的掉宝读取能力", "capability_unavailable", 409)
		}
		return nil, fault("Twitch 接口暂不可用或已变更，保留上次确认结果", "capability_unavailable", 409)
	}
	return obj(r["data"]), nil
}
func (t *Twitch) game(ctx context.Context, name string) (M, error) {
	r, e := t.gql(ctx, nil, "query($name:String!){game(name:$name){id name slug}}", M{"name": name}, "")
	if e != nil {
		return nil, e
	}
	g := obj(r["game"])
	if len(g) == 0 {
		return nil, fault("没有找到游戏，请填写 Twitch 上的完整游戏名称", "game_not_found", 400)
	}
	return g, nil
}
func (t *Twitch) channel(ctx context.Context, login string) (M, error) {
	r, e := t.gql(ctx, nil, "query($login:String!){user(login:$login){id login displayName stream{id game{id name slug}}}}", M{"login": login}, "")
	if e != nil {
		return nil, e
	}
	return obj(r["user"]), nil
}
func (t *Twitch) channels(ctx context.Context, logins []any) ([]any, error) {
	if len(logins) == 0 {
		return []any{}, nil
	}
	logins = logins[:min(12, len(logins))]
	vars := M{}
	args, fields := []string{}, []string{}
	for i, v := range logins {
		k := "login" + text(i)
		vars[k] = v
		args = append(args, "$"+k+":String!")
		fields = append(fields, "channel"+text(i)+":user(login:$"+k+"){id login displayName stream{id game{id name slug}}}")
	}
	r, e := t.gql(ctx, nil, "query("+strings.Join(args, ",")+"){"+strings.Join(fields, " ")+"}", vars, "")
	if e != nil {
		return nil, e
	}
	result := []any{}
	for i := range logins {
		result = append(result, r["channel"+text(i)])
	}
	return result, nil
}
func (t *Twitch) inventory(ctx context.Context, account M) (M, error) {
	r, e := t.gql(ctx, account, "", M{"fetchRewardCampaigns": false}, "Inventory")
	if e != nil {
		return nil, e
	}
	inventory := obj(obj(r["currentUser"])["inventory"])
	if len(inventory) == 0 {
		return nil, fault("Twitch 暂未返回此账号的掉宝库存", "capability_unavailable", 409)
	}
	if _, ok := inventory["dropCampaignsInProgress"].([]any); !ok {
		return nil, fault("Twitch 未返回账号的掉宝进度；不能视为没有活动或零进度", "capability_unavailable", 409)
	}
	return inventory, nil
}
func (t *Twitch) campaigns(ctx context.Context, account M) ([]any, error) {
	r, e := t.gql(ctx, account, "", M{"fetchRewardCampaigns": false}, "Campaigns")
	if e != nil {
		return nil, e
	}
	a, ok := obj(r["currentUser"])["dropCampaigns"].([]any)
	if !ok {
		return nil, fault("Twitch 暂未返回掉宝活动目录", "capability_unavailable", 409)
	}
	return a, nil
}
func (t *Twitch) details(ctx context.Context, account M, id string) (M, error) {
	r, e := t.gql(ctx, account, "", M{"channelLogin": account["login"], "dropID": id}, "Details")
	if e != nil {
		return nil, e
	}
	return obj(obj(r["user"])["dropCampaign"]), nil
}
func (t *Twitch) current(ctx context.Context, account, channel M) (M, error) {
	r, e := t.gql(ctx, account, "", M{"channelID": text(channel["id"]), "channelLogin": ""}, "Current")
	if e != nil {
		return nil, e
	}
	current := obj(obj(r["currentUser"])["dropCurrentSession"])
	if text(current["dropID"]) == "" || !validNumber(current["currentMinutesWatched"]) {
		return nil, nil
	}
	return current, nil
}
func (t *Twitch) available(ctx context.Context, account, channel M) ([]any, error) {
	r, e := t.gql(ctx, account, "", M{"channelID": text(channel["id"])}, "Available")
	if e != nil {
		return nil, e
	}
	return list(obj(r["channel"])["viewerDropCampaigns"]), nil
}
func (t *Twitch) directory(ctx context.Context, game M) ([]any, error) {
	q := `query($slug:String!){game(slug:$slug){streams(first:10,options:{systemFilters:[DROPS_ENABLED],sort:VIEWER_COUNT}){edges{node{broadcaster{id login displayName}}}}}}`
	r, e := t.gql(ctx, nil, q, M{"slug": game["slug"]}, "")
	if e != nil {
		return nil, e
	}
	a := []any{}
	for _, v := range list(obj(obj(r["game"])["streams"])["edges"]) {
		login := obj(obj(obj(v)["node"])["broadcaster"])["login"]
		if text(login) != "" {
			a = append(a, login)
		}
	}
	return a, nil
}
