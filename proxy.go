package main

import (
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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
	var node any
	for _, v := range list(config["proxies"]) {
		if text(obj(v)["name"]) == p.Node {
			node = v
			break
		}
	}
	if node == nil {
		return errors.New("selected proxy node missing")
	}
	if p.dir != "" {
		os.RemoveAll(p.dir)
	}
	p.dir, e = os.MkdirTemp("", "tdm-proxy-*")
	if e != nil {
		return e
	}
	runtime := M{"mixed-port": p.Port, "bind-address": "127.0.0.1", "allow-lan": false, "mode": "rule", "log-level": "silent", "ipv6": false, "external-controller": "127.0.0.1:" + text(p.Controller), "secret": p.Secret, "proxies": []any{node}, "rules": []any{"MATCH," + p.Node}}
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
	return nil
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
