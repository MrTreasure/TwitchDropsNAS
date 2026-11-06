package main

import (
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Proxy struct {
	mu                               sync.Mutex
	URL, Source, Node, Label, Secret string
	Port, Controller                 int
	cmd                              *exec.Cmd
	done                             chan struct{}
	dir                              string
}

func newProxy() (*Proxy, error) {
	p := &Proxy{URL: os.Getenv("TDM_PROXY_URL"), Source: os.Getenv("TDM_PROXY_SOURCE"), Node: os.Getenv("TDM_PROXY_NODE"), Port: envInt("TDM_PROXY_PORT", 28197), Secret: randomID(32)}
	if p.Source != "" && p.URL != "" {
		return nil, errors.New("choose one explicit proxy")
	}
	if p.Source != "" {
		if p.Node == "" {
			return nil, errors.New("explicit proxy node required")
		}
		p.Controller = envInt("TDM_PROXY_CONTROLLER_PORT", 28198)
		p.URL = "http://127.0.0.1:" + text(p.Port)
	}
	if p.URL != "" {
		u, e := url.Parse(p.URL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return nil, errors.New("invalid HTTP proxy")
		}
	}
	label := "未配置代理"
	if p.URL != "" {
		label = "已配置代理"
	}
	p.Label = truncate(env("TDM_PROXY_LABEL", label), 100)
	return p, nil
}
func (p *Proxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Source == "" {
		return nil
	}
	if p.cmd != nil {
		select {
		case <-p.done:
			p.cmd = nil
		default:
			return nil
		}
	}
	raw, e := os.ReadFile(p.Source)
	if e != nil {
		return e
	}
	var config M
	if yaml.Unmarshal(raw, &config) != nil {
		return errors.New("invalid proxy source")
	}
	selection, e := selectedProxyConfig(config, p.Node)
	if e != nil {
		return e
	}
	if p.dir != "" {
		os.RemoveAll(p.dir)
	}
	p.dir, e = os.MkdirTemp("", "tdm-proxy-*")
	if e != nil {
		return e
	}
	runtime := M{"mixed-port": p.Port, "bind-address": "127.0.0.1", "allow-lan": false, "mode": "rule", "log-level": "silent", "ipv6": false, "external-controller": "127.0.0.1:" + text(p.Controller), "secret": p.Secret, "proxies": selection["proxies"], "proxy-groups": selection["proxy-groups"], "rules": []any{"MATCH," + p.Node}}
	data, e := yaml.Marshal(runtime)
	if e != nil {
		return e
	}
	cmd, e := memoryProxyCommand(env("TDM_MIHOMO", "/usr/local/bin/mihomo"), p.dir, data)
	if e != nil {
		return e
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		closeProxyFiles(cmd)
		return e
	}
	closeProxyFiles(cmd)
	p.cmd = cmd
	p.done = make(chan struct{})
	go func() { _ = cmd.Wait(); close(p.done) }()
	// Do not dispatch the first Twitch request before the local proxy listens.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return errors.New("proxy exited before readiness")
		default:
		}
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", text(p.Port)), 100*time.Millisecond)
		if err == nil {
			connection.Close()
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("proxy listener not ready")
}
func (p *Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		_ = p.cmd.Process.Kill()
		<-p.done
		p.cmd = nil
	}
	if p.dir != "" {
		_ = os.RemoveAll(filepath.Clean(p.dir))
	}
}

// Copy only the explicitly selected node/group and its dependencies.
// The host's listeners, routing rules and unrelated proxy credentials stay out.
func selectedProxyConfig(config M, selected string) (M, error) {
	nodes, groups := map[string]M{}, map[string]M{}
	for _, raw := range list(config["proxies"]) {
		row := obj(raw)
		nodes[text(row["name"])] = row
	}
	for _, raw := range list(config["proxy-groups"]) {
		row := obj(raw)
		groups[text(row["name"])] = row
	}
	outNodes, outGroups := []any{}, []any{}
	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if name == "DIRECT" || name == "REJECT" || name == "REJECT-DROP" {
			return nil
		}
		if active[name] {
			return errors.New("cyclic proxy dependency")
		}
		if visited[name] {
			return nil
		}
		active[name] = true
		if node := nodes[name]; node != nil {
			if dependency := text(node["dialer-proxy"]); dependency != "" {
				if err := visit(dependency); err != nil {
					return err
				}
			}
			outNodes = append(outNodes, node)
		} else if group := groups[name]; group != nil {
			if len(list(group["use"])) > 0 {
				return errors.New("proxy provider groups are not supported")
			}
			if len(list(group["proxies"])) == 0 {
				return errors.New("empty proxy group")
			}
			for _, dependency := range list(group["proxies"]) {
				if err := visit(text(dependency)); err != nil {
					return err
				}
			}
			outGroups = append(outGroups, group)
		} else {
			return errors.New("selected proxy dependency missing")
		}
		active[name] = false
		visited[name] = true
		return nil
	}
	if selected == "DIRECT" || selected == "REJECT" || selected == "REJECT-DROP" {
		return nil, errors.New("a proxy node or group is required")
	}
	if err := visit(selected); err != nil {
		return nil, err
	}
	return M{"proxies": outNodes, "proxy-groups": outGroups}, nil
}
