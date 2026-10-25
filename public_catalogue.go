package main

import "context"

const publicCatalogueURL = "https://twitch-drops-api.sunkwi.com/v2/drops"

// Public discovery metadata is never account state or proof of completion.
func parsePublicCatalogue(payload M, at float64) ([]any, error) {
	updated := timestamp(payload["lastUpdatedAt"])
	interval := number(payload["refreshIntervalSeconds"])
	groups, ok := payload["data"].([]any)
	if !ok || len(groups) > 1000 || updated <= 0 || interval < 1 || interval > 3600 || updated > at+120 || at-updated > max(300, interval*5) {
		return nil, fault("公共活动目录过期或格式无效，保留已确认的 Twitch 数据", "catalogue_unavailable", 409)
	}
	items, seen := []any{}, map[string]bool{}
	for _, v := range groups {
		for _, v := range list(obj(v)["rewards"]) {
			raw := obj(v)
			id := text(raw["id"])
			if id == "" || len(id) > 200 || seen[id] || len(items) >= 1000 || (raw["status"] != "ACTIVE" && raw["status"] != "UPCOMING") || timestamp(raw["startAt"]) <= 0 || timestamp(raw["endAt"]) <= at || timestamp(raw["endAt"]) <= timestamp(raw["startAt"]) {
				continue
			}
			raw = obj(clone(raw))
			game := obj(raw["game"])
			game["name"] = first(game["name"], game["displayName"])
			raw["game"] = game
			c := cleanCampaign(raw, text(payload["lastUpdatedAt"]))
			if text(c["gameId"]) == "" || text(c["gameName"]) == "" || len(list(c["drops"])) == 0 {
				continue
			}
			seen[id] = true
			c["officialConfirmed"], c["metadataSource"], c["linked"] = false, "public_catalogue", nil
			for _, v := range list(c["drops"]) {
				d := obj(v)
				d["catalogueRequiredMinutes"] = d["requiredMinutes"]
				d["minutes"], d["requiredMinutes"], d["claimed"], d["claimable"] = nil, nil, nil, false
			}
			items = append(items, c)
		}
	}
	return items, nil
}

// Called by the existing metadata executor only. At most one anonymous JSON
// fetch per hour (including manual sync); failures back off for ten minutes.
func (s *Service) refreshPublicCatalogue(ctx context.Context) error {
	cache := obj(s.store.Get("publicCatalogueCache", M{}))
	if number(cache["schemaVersion"]) == 1 && unixNow()-number(cache["fetchedAt"]) < 3600 {
		return nil
	}
	if unixNow()-number(s.store.Get("publicCatalogueFailedAt", 0)) < 600 {
		return fault("公共目录暂不可用，已保留缓存，稍后再试", "catalogue_unavailable", 409)
	}
	status, payload, err := s.net.request(ctx, "GET", publicCatalogueURL, M{"Accept": "application/json"}, nil, nil, 2097152)
	var items []any
	if err == nil && status != 200 {
		err = fault("公共活动目录请求失败，已保留缓存", "catalogue_unavailable", 502)
	}
	if err == nil {
		items, err = parsePublicCatalogue(payload, unixNow())
	}
	if err != nil {
		s.store.PutMany(M{"publicCatalogueFailedAt": unixNow(), "publicCatalogueError": safeError(err)})
		return err
	}
	s.store.PutMany(M{"publicCatalogueCache": M{"schemaVersion": 1, "fetchedAt": unixNow(), "sampledAt": payload["lastUpdatedAt"], "items": items}, "publicCatalogueFailedAt": 0, "publicCatalogueError": nil})
	return nil
}

func (s *Service) directoryStatusLocked() M {
	cache := obj(s.store.Get("publicCatalogueCache", M{}))
	return M{"publicSource": publicCatalogueURL, "publicSampledAt": cache["sampledAt"], "publicFetchedAt": cache["fetchedAt"], "publicAvailable": len(list(cache["items"])) > 0, "publicError": s.store.Get("publicCatalogueError", nil), "officialMessage": obj(s.store.Get("progress", M{}))["catalogueMessage"], "refreshSeconds": 3600}
}

// AvailableDrops is an official live-channel response. Supply a missing game
// from the official stream, never from public metadata; retain unknown account
// linkage and progress. Unknown watch requirements remain unknown.
func confirmedChannelCampaign(raw, channel M) M {
	raw = obj(clone(raw))
	if text(obj(raw["game"])["id"]) == "" {
		raw["game"] = obj(obj(channel["stream"])["game"])
	}
	if timestamp(raw["startAt"]) <= 0 {
		for _, v := range list(raw["timeBasedDrops"]) {
			start := obj(v)["startAt"]
			if timestamp(start) > 0 && (timestamp(raw["startAt"]) <= 0 || timestamp(start) < timestamp(raw["startAt"])) {
				raw["startAt"] = start
			}
		}
	}
	raw["allow"] = M{"isEnabled": true, "channels": []any{M{"id": channel["id"], "login": channel["login"]}}}
	c := cleanCampaign(raw, now())
	c["officialConfirmed"], c["metadataSource"], c["metadataFetchedAt"] = true, "twitch_channel", now()
	return c
}
