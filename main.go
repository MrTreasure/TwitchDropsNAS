package main

import (
	"bufio"
	"context"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "Twitch Drops operation failed; inspect private configuration and file permissions.")
			os.Exit(1)
		}
	}()
	privateUmask()
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "--web-ui") {
		command = args[0]
		args = args[1:]
	}
	var e error
	switch command {
	case "version", "--version":
		fmt.Println("TwitchDropsNAS " + version)
		return
	case "health", "sync":
		e = localCommand(command)
	case "serve":
		e = configureWebUI(args)
		if e == nil {
			e = serve()
		}
	default:
		e = errors.New("unknown command")
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "Twitch Drops operation failed; inspect service status and private configuration.")
		os.Exit(1)
	}
}

// Native interactive launches offer a choice; unattended launches use flags/env.
func configureWebUI(args []string) error {
	raw, configured := os.LookupEnv("TDM_WEB_UI")
	if configured && raw != "true" && raw != "false" {
		return errors.New("invalid web UI option")
	}
	enabled := raw != "false"
	flags := stdflag.NewFlagSet("serve", stdflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&enabled, "web-ui", enabled, "serve the standalone Web UI")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unknown startup option")
	}
	explicit := configured || len(args) > 0 || envInt("TDM_HTTP_PORT", 8080) == 0
	if !explicit {
		input, ie := os.Stdin.Stat()
		output, oe := os.Stdout.Stat()
		if ie == nil && oe == nil && input.Mode()&os.ModeCharDevice != 0 && output.Mode()&os.ModeCharDevice != 0 {
			fmt.Print("Start standalone Web UI? [Y/n] / 启动独立 Web UI？[Y/n]: ")
			answer, e := bufio.NewReader(os.Stdin).ReadString('\n')
			if e != nil && e != io.EOF {
				return e
			}
			answer = strings.ToLower(strings.TrimSpace(answer))
			switch answer {
			case "", "y", "yes", "是":
				enabled = true
			case "n", "no", "否":
				enabled = false
			default:
				return errors.New("invalid web UI choice")
			}
		}
	}
	return os.Setenv("TDM_WEB_UI", strconv.FormatBool(enabled))
}

func localCommand(command string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 185*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 185 * time.Second}
	destination := "http://127.0.0.1:" + text(envInt("TDM_HTTP_PORT", 8080))
	method, p := "GET", "/health"
	if command == "sync" {
		method = "POST"
		p = "/internal/scan"
	}
	socket := os.Getenv("TDM_SOCKET")
	if socket != "" {
		client.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
		destination = "http://localhost"
	} else if command == "sync" {
		return errors.New("native scheduler sync requires an explicitly configured Unix socket")
	}
	req, e := http.NewRequestWithContext(ctx, method, destination+p, nil)
	if e != nil {
		return e
	}
	if command == "sync" {
		req.Body = io.NopCloser(strings.NewReader("{}"))
		req.ContentLength = 2
		req.Header.Set("Content-Type", "application/json")
	}
	r, e := client.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	_, e = io.Copy(io.Discard, io.LimitReader(r.Body, 32768))
	if e != nil {
		return e
	}
	if r.StatusCode != 200 {
		return errors.New("local command rejected")
	}
	if command == "sync" {
		fmt.Println("Official state check finished; inspect source timestamps and capabilities.")
	}
	return nil
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir := env("TDM_DATA_DIR", "./data")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	unlock, e := lockInstance(filepath.Join(dir, ".service.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	s, e := newService(ctx)
	if e != nil {
		return e
	}
	defer s.close()
	w, e := newWeb(s)
	if e != nil {
		return e
	}
	servers := []*http.Server{}
	listeners := []net.Listener{}
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	makeServer := func(l net.Listener, trusted bool) {
		listeners = append(listeners, l)
		servers = append(servers, &http.Server{Handler: w.handler(trusted), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }})
	}
	if socket := os.Getenv("TDM_SOCKET"); socket != "" {
		if e = os.MkdirAll(filepath.Dir(socket), 0700); e != nil {
			return e
		}
		if st, se := os.Lstat(socket); se == nil {
			if st.Mode()&os.ModeSocket == 0 {
				return errors.New("socket path is not a socket")
			}
			c, ce := net.DialTimeout("unix", socket, time.Second)
			if ce == nil {
				c.Close()
				return errors.New("socket already in use")
			}
			if e = os.Remove(socket); e != nil {
				return e
			}
		}
		l, le := net.Listen("unix", socket)
		if le != nil {
			return le
		}
		if e = os.Chmod(socket, 0600); e != nil {
			l.Close()
			return e
		}
		makeServer(l, true)
	}
	if port := envInt("TDM_HTTP_PORT", 8080); port != 0 {
		l, le := net.Listen("tcp", net.JoinHostPort(env("TDM_HTTP_HOST", "127.0.0.1"), text(port)))
		if le != nil {
			return le
		}
		makeServer(l, false)
	}
	if len(listeners) == 0 {
		return errors.New("no API listener configured")
	}
	if e = s.start(); e != nil {
		return e
	}
	failures := make(chan error, len(servers))
	for i, server := range servers {
		go func(server *http.Server, l net.Listener) { failures <- server.Serve(l) }(server, listeners[i])
	}
	select {
	case <-ctx.Done():
	case e = <-failures:
		if e == http.ErrServerClosed {
			e = nil
		}
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		_ = server.Shutdown(shutdown)
	}
	return e
}
