package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

var version = "2.0.0"

type M = map[string]any

func obj(v any) M {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return M{}
}
func list(v any) []any {
	if a, ok := v.([]any); ok && a != nil {
		return a
	}
	return []any{}
}
func text(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func flag(v any) bool { b, _ := v.(bool); return b }
func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		n, _ := x.Float64()
		return n
	}
	return 0
}
func validNumber(v any) bool {
	switch v.(type) {
	case float64, int, int64, json.Number:
		n := number(v)
		return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	return false
}
func integer(v any) bool { return validNumber(v) && math.Trunc(number(v)) == number(v) }
func value(m M, k string, fallback any) any {
	v, ok := m[k]
	if ok {
		return v
	}
	return fallback
}
func first(vs ...any) any {
	for _, v := range vs {
		if v != nil && v != "" {
			return v
		}
	}
	return nil
}
func clone(v any) any {
	b, e := json.Marshal(v)
	must(e)
	var r any
	must(json.Unmarshal(b, &r))
	return r
}
func bytesJSON(v any) []byte { b, e := json.Marshal(v); must(e); return b }
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func unixNow() float64 { return float64(time.Now().UnixNano()) / 1e9 }
func now() string      { return time.Now().UTC().Format(time.RFC3339Nano) }
func iso(t float64) any {
	if t <= 0 {
		return nil
	}
	return time.Unix(0, int64(t*1e9)).UTC().Format(time.RFC3339Nano)
}
func timestamp(v any) float64 {
	t, e := time.Parse(time.RFC3339Nano, text(v))
	if e != nil {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}
func env(k, d string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return d
}
func envInt(k string, d int) int {
	n, e := strconv.Atoi(env(k, strconv.Itoa(d)))
	if e != nil {
		panic(errors.New("invalid integer configuration"))
	}
	return n
}
func envBool(k string) bool { return strings.EqualFold(os.Getenv(k), "true") }
func randomID(n int) string {
	b := make([]byte, n)
	_, e := rand.Read(b)
	must(e)
	return hex.EncodeToString(b)
}
func unique(a []any) []any {
	r := []any{}
	seen := map[string]bool{}
	for _, v := range a {
		k := text(v)
		if !seen[k] {
			seen[k] = true
			r = append(r, v)
		}
	}
	return r
}
func contains(a []any, s string) bool {
	for _, v := range a {
		if text(v) == s {
			return true
		}
	}
	return false
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

type serviceError struct {
	Message, Code string
	Status        int
}

func (e *serviceError) Error() string              { return e.Message }
func fail(message string) error                    { return &serviceError{message, "upstream_error", 400} }
func fault(message, code string, status int) error { return &serviceError{message, code, status} }
func errorCode(e error) string {
	var s *serviceError
	if errors.As(e, &s) {
		return s.Code
	}
	return "internal_error"
}
func safeError(e error) string {
	var s *serviceError
	if errors.As(e, &s) {
		return s.Message
	}
	return "操作失败，原有数据已保留"
}
