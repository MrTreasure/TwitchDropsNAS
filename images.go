package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var cdnHosts = map[string]bool{"static-cdn.jtvnw.net": true, "images.twitch.tv": true, "static.twitchcdn.net": true, "staticfiles.warthunder.com": true, "patchnotes.cdn.gaijin.net": true}

func rewardImageURL(v any) any {
	s, ok := v.(string)
	if !ok || len(s) > 2048 || strings.IndexFunc(s, func(r rune) bool { return r < 33 }) >= 0 {
		return nil
	}
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || !cdnHosts[u.Hostname()] || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil
	}
	return s
}
func normalized(v any) string {
	s := norm.NFKC.String(text(v))
	s = strings.NewReplacer("“", "\"", "”", "\"", "‘", "'", "’", "'").Replace(s)
	return cases.Fold().String(strings.Join(strings.Fields(s), " "))
}
func imageKey(v any) string {
	h := sha256.Sum256([]byte(normalized(v)))
	return hex.EncodeToString(h[:])[:24]
}

type imageFrame struct {
	tag, text string
	images    []int
}

func parseHTMLImages(raw string) ([]any, error) {
	z := html.NewTokenizer(strings.NewReader(raw))
	entries := []any{}
	frames := []imageFrame{}
	ignored := 0
	closeTag := func(tag string) {
		found := -1
		for i := len(frames) - 1; i >= 0; i-- {
			if frames[i].tag == tag {
				found = i
				break
			}
		}
		if found < 0 {
			return
		}
		for i := len(frames) - 1; i >= found; i-- {
			f := frames[i]
			label := strings.Join(strings.Fields(f.text), " ")
			if label != "" && len([]rune(label)) <= 800 {
				for _, index := range f.images {
					m := obj(entries[index])
					a := list(m["contexts"])
					if len(a) < 6 && !contains(a, label) {
						m["contexts"] = append(a, label)
					}
				}
			}
		}
		frames = frames[:found]
	}
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				return nil, fail("HTML 图片资料无法解析")
			}
			break
		}
		t := z.Token()
		tag := t.Data
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			if tag == "script" || tag == "style" {
				ignored++
			}
			if ignored > 0 {
				continue
			}
			if tag == "img" {
				if len(entries) >= 200 {
					return nil, fail("一次最多导入 200 张图片")
				}
				attrs := map[string]string{}
				for _, a := range t.Attr {
					attrs[a.Key] = a.Val
				}
				entries = append(entries, M{"name": attrs["alt"], "imageUrl": first(attrs["src"], attrs["data-src"]), "contexts": []any{}})
				for i := max(0, len(frames)-6); i < len(frames); i++ {
					frames[i].images = append(frames[i].images, len(entries)-1)
				}
			} else if !strings.Contains(" br hr input meta link source wbr area base col embed param track ", " "+tag+" ") {
				if len(frames) >= 64 {
					return nil, fail("HTML 嵌套过深，请只复制奖励区域或使用 JSON")
				}
				frames = append(frames, imageFrame{tag: tag})
				if tt == html.SelfClosingTagToken {
					closeTag(tag)
				}
			}
		case html.TextToken:
			if ignored == 0 {
				for i := range frames {
					if len([]rune(frames[i].text)) < 800 {
						frames[i].text += " " + truncate(t.Data, 800)
					}
				}
			}
		case html.EndTagToken:
			if (tag == "script" || tag == "style") && ignored > 0 {
				ignored--
				continue
			}
			if ignored == 0 {
				closeTag(tag)
			}
		}
	}
	return entries, nil
}
func parseImages(raw any) ([]any, error) {
	var entries []any
	if s, ok := raw.(string); ok {
		if len(s) > 16000 {
			return nil, fail("图片资料最多 16 KiB，请只展开目标活动后重新提取")
		}
		if strings.HasPrefix(strings.TrimSpace(s), "<") {
			a, e := parseHTMLImages(s)
			if e != nil {
				return nil, e
			}
			entries = a
		} else {
			if json.Unmarshal([]byte(s), &entries) != nil {
				return nil, fail("请粘贴图片 JSON 或奖励区域 HTML")
			}
		}
	} else {
		a, ok := raw.([]any)
		if !ok {
			return nil, fail("图片资料需为 1–200 个项目的 JSON 数组或含图片的 HTML")
		}
		entries = a
	}
	if len(entries) < 1 || len(entries) > 200 {
		return nil, fail("图片资料需为 1–200 个项目的 JSON 数组或含图片的 HTML")
	}
	if len(bytesJSON(entries)) > 24000 {
		return nil, fail("图片资料过大，请只提取目标活动")
	}
	return entries, nil
}
func aliasInContext(context, alias string) bool {
	for start := 0; start <= len(context); {
		n := strings.Index(context[start:], alias)
		if n < 0 {
			return false
		}
		n += start
		end := n + len(alias)
		left := []rune(context[:n])
		right := []rune(context[end:])
		word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }
		if (len(left) == 0 || !word(left[len(left)-1])) && (len(right) == 0 || !word(right[0])) {
			return true
		}
		start = n + 1
	}
	return false
}
func previewImport(scope M, raw any) (M, error) {
	entries, e := parseImages(raw)
	if e != nil {
		return nil, e
	}
	names := map[string]map[string]bool{}
	byKey := map[string]M{}
	for _, v := range list(scope["rewards"]) {
		r := obj(v)
		key := text(r["key"])
		byKey[key] = r
		aliases := append([]any{r["name"], r["label"]}, list(r["aliases"])...)
		for _, a := range aliases {
			n := normalized(a)
			if n != "" {
				if names[n] == nil {
					names[n] = map[string]bool{}
				}
				names[n][key] = true
			}
		}
	}
	matches, unmatched, invalid, conflicts := []any{}, []any{}, []any{}, []any{}
	candidates := map[string]map[string]string{}
	order := []string{}
	for i, v := range entries {
		entry, ok := v.(map[string]any)
		if !ok {
			invalid = append(invalid, M{"index": i + 1, "reason": "图片项目必须为对象"})
			continue
		}
		name := truncate(text(entry["name"]), 300)
		u := rewardImageURL(first(entry["imageUrl"], entry["imageAssetURL"], entry["url"], entry["src"]))
		if u == nil {
			invalid = append(invalid, M{"index": i + 1, "reason": "只接受已允许的官方 HTTPS CDN 图片地址"})
			continue
		}
		key, hasKey := entry["rewardKey"]
		if hasKey && key != nil {
			ks, isString := key.(string)
			if !isString || byKey[ks] == nil {
				invalid = append(invalid, M{"index": i + 1, "reason": "奖励对应关系已失效，请重新预览"})
				continue
			}
		}
		found := names[normalized(name)]
		if key != nil {
			found = map[string]bool{text(key): true}
		}
		method := "name"
		if len(found) == 0 {
			contexts := list(value(entry, "contexts", []any{value(entry, "context", "")}))
			for j, t := range contexts {
				if j >= 6 {
					break
				}
				label := normalized(truncate(text(t), 800))
				possible := map[string]bool{}
				for alias, keys := range names {
					if aliasInContext(label, alias) {
						for k := range keys {
							possible[k] = true
						}
					}
				}
				if len(possible) > 0 {
					found = possible
					method = "context"
					break
				}
			}
		}
		if len(found) != 1 {
			reason := "不属于当前活动或缺少准确名称"
			if len(found) > 0 {
				reason = "对应多个奖励，请补充准确名称"
			}
			unmatched = append(unmatched, M{"name": first(name, "未命名图片"), "reason": reason})
			continue
		}
		for k := range found {
			if candidates[k] == nil {
				candidates[k] = map[string]string{}
				order = append(order, k)
			}
			candidates[k][text(u)] = method
		}
	}
	for _, k := range order {
		urls := candidates[k]
		r := byKey[k]
		if len(urls) != 1 {
			conflicts = append(conflicts, M{"name": r["label"], "reason": "同一奖励对应多个不同地址，请保留一个"})
			continue
		}
		for u, method := range urls {
			matches = append(matches, M{"rewardKey": k, "name": r["label"], "imageUrl": u, "previousImageUrl": r["imageUrl"], "matchedBy": method})
		}
	}
	return M{"scope": scope["id"], "scopeName": scope["name"], "matches": matches, "unmatched": unmatched, "invalid": invalid, "conflicts": conflicts, "canApply": len(matches) > 0 && len(conflicts) == 0 && len(invalid) == 0}, nil
}
func scopeDefinitions(announcements, campaigns []any) []any {
	r := []any{}
	for _, v := range announcements {
		a := obj(v)
		rewards := []any{}
		for _, v := range list(a["rewards"]) {
			b := obj(v)
			rewards = append(rewards, M{"key": imageKey(b["name"]), "name": b["name"], "label": first(b["label"], b["name"]), "aliases": list(b["aliases"]), "imageUrl": b["imageUrl"]})
		}
		r = append(r, M{"id": "announcement:" + text(a["id"]), "name": a["name"], "gameName": a["gameName"], "rewards": rewards})
	}
	for _, v := range campaigns {
		c := obj(v)
		rewards := []any{}
		for _, v := range list(c["drops"]) {
			d := obj(v)
			benefits := list(d["benefits"])
			if len(benefits) == 0 {
				benefits = []any{d["name"]}
			}
			benefits = unique(benefits)
			for _, name := range benefits {
				aliases := []any{}
				if len(benefits) == 1 {
					aliases = append(aliases, d["name"])
				}
				var u any
				for _, v := range list(d["images"]) {
					im := obj(v)
					if text(im["name"]) == text(name) {
						u = im["url"]
						break
					}
				}
				rewards = append(rewards, M{"key": text(d["id"]) + ":" + imageKey(name), "name": name, "label": name, "aliases": aliases, "imageUrl": u})
			}
		}
		r = append(r, M{"id": "campaign:" + text(c["id"]), "name": c["name"], "gameName": c["gameName"], "rewards": rewards})
	}
	return r
}
func applyImport(store *Store, plan M) (int, error) {
	if !flag(plan["canApply"]) {
		return 0, fault("请先处理预览中的无效地址或冲突，并确保至少匹配一个奖励", "image_import_conflict", 409)
	}
	registry := obj(store.Get("rewardImageRegistry", M{}))
	id := text(plan["scope"])
	if registry[id] == nil && len(registry) >= 500 {
		return 0, fail("图片活动记录已达上限，请联系部署管理员归档旧图片资料")
	}
	scope := obj(registry[id])
	for _, v := range list(plan["matches"]) {
		m := obj(v)
		scope[text(m["rewardKey"])] = M{"url": m["imageUrl"], "importedAt": now(), "source": "manual-page-import"}
	}
	registry[id] = scope
	store.Put("rewardImageRegistry", registry)
	return len(list(plan["matches"])), nil
}
func resolveImages(announcements, campaigns []any, registry M) ([]any, []any) {
	a, c := list(clone(announcements)), list(clone(campaigns))
	for _, v := range a {
		item := obj(v)
		images := obj(registry["announcement:"+text(item["id"])])
		for _, v := range list(item["rewards"]) {
			r := obj(v)
			saved := obj(images[imageKey(r["name"])])
			if u := rewardImageURL(saved["url"]); u != nil {
				r["imageUrl"] = u
				r["imageSource"] = saved["source"]
				r["imageImportedAt"] = saved["importedAt"]
			}
		}
	}
	for _, v := range c {
		campaign := obj(v)
		images := obj(registry["campaign:"+text(campaign["id"])])
		for _, v := range list(campaign["drops"]) {
			d := obj(v)
			native := map[string]M{}
			order := []string{}
			for _, v := range list(d["images"]) {
				im := obj(v)
				if rewardImageURL(im["url"]) != nil {
					name := text(im["name"])
					if native[name] == nil {
						order = append(order, name)
					}
					native[name] = im
				}
			}
			benefits := list(d["benefits"])
			if len(benefits) == 0 {
				benefits = []any{d["name"]}
			}
			for _, name := range benefits {
				n := text(name)
				saved := obj(images[text(d["id"])+":"+imageKey(n)])
				if u := rewardImageURL(saved["url"]); native[n] == nil && u != nil {
					native[n] = M{"name": n, "url": u, "source": saved["source"]}
					order = append(order, n)
				}
			}
			out := []any{}
			for _, n := range order {
				out = append(out, native[n])
			}
			d["images"] = out
		}
	}
	return a, c
}

var channelPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,25}$`)
