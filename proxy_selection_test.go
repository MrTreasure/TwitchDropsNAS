package main

import "testing"

func TestSelectedProxyGroupDependencies(t *testing.T) {
	config := M{"proxies": []any{M{"name": "us", "dialer-proxy": "relay"}, M{"name": "relay"}, M{"name": "unrelated", "password": "must-not-copy"}}, "proxy-groups": []any{M{"name": "US-Fixed", "type": "select", "proxies": []any{"nested", "us"}}, M{"name": "nested", "type": "select", "proxies": []any{"us"}}}}
	selected, err := selectedProxyConfig(config, "US-Fixed")
	if err != nil {
		t.Fatal(err)
	}
	if len(list(selected["proxies"])) != 2 || len(list(selected["proxy-groups"])) != 2 {
		t.Fatal("incorrect dependency closure")
	}
	for _, v := range list(selected["proxies"]) {
		if obj(v)["name"] == "unrelated" {
			t.Fatal("unrelated credentials copied")
		}
	}
	if _, err = selectedProxyConfig(config, "missing"); err == nil {
		t.Fatal("missing node accepted")
	}
	if _, err = selectedProxyConfig(config, "DIRECT"); err == nil {
		t.Fatal("direct root accepted")
	}
	config["proxy-groups"] = []any{M{"name": "loop", "proxies": []any{"loop"}}}
	if _, err = selectedProxyConfig(config, "loop"); err == nil {
		t.Fatal("cycle accepted")
	}
}
