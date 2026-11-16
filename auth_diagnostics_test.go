package main

import (
    "bytes"
    "encoding/json"
    "log/slog"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"
)

func TestSessionRejectionReasons(t *testing.T) {
    a := testApp(t)
    value := strings.Repeat("s", 43)
    request := func(raw string) *http.Request {
        r := httptest.NewRequest("GET", "/api/session", nil)
        if raw != "" {
            r.AddCookie(&http.Cookie{Name: "__Host-walter-session", Value: raw})
        }
        return r
    }
    if _, reason := a.auth.sessionWithReason(request("")); reason != "cookie_missing" {
        t.Fatal(reason)
    }
    if _, reason := a.auth.sessionWithReason(request("bad")); reason != "cookie_invalid" {
        t.Fatal(reason)
    }
    if _, reason := a.auth.sessionWithReason(request(value)); reason != "session_unknown" {
        t.Fatal(reason)
    }
    a.auth.sessions[hashToken(value)] = Session{User: "a", Expires: time.Now().Add(-time.Second)}
    if _, reason := a.auth.sessionWithReason(request(value)); reason != "session_expired" {
        t.Fatal(reason)
    }
    a.auth.sessions[hashToken(value)] = Session{User: "a", Expires: time.Now().Add(time.Hour)}
    if s, reason := a.auth.sessionWithReason(request(value)); s == nil || reason != "" {
        t.Fatal(s, reason)
    }
}

func TestAuthFailureDiagnosticCorrelationAndRedaction(t *testing.T) {
    a := testApp(t)
    var output bytes.Buffer
    previous := slog.Default()
    slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelError})))
    t.Cleanup(func() { slog.SetDefault(previous) })
    handler := a.routes()
    r := httptest.NewRequest("POST", "/api/conversations/SECRET/messages?token=SECRET", strings.NewReader("{}"))
    r.Header.Set("Origin", a.cfg.PublicURL)
    r.Header.Set("X-CSRF-Token", "SECRET")
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, r)
    if w.Code != 401 {
        t.Fatal(w.Code, w.Body.String())
    }
    if strings.Contains(output.String(), "SECRET") {
        t.Fatal("sensitive request data logged", output.String())
    }
    var route, reason, status bool
    for _, record := range diagnosticRecords(t, &output) {
        switch record["msg"] {
        case "request routed":
            route = record["route"] == "POST /api/conversations/{id}/messages"
        case "authentication rejected":
            reason = record["auth_reason"] == "cookie_missing"
        case "request completed":
            status = record["status_code"] == float64(401)
        }
    }
    if !route || !reason || !status {
        t.Fatal("missing route/auth/status diagnostics", output.String())
    }
}

func TestDiagnosticEnumRedaction(t *testing.T) {
    r := slog.NewRecord(time.Now(), slog.LevelDebug, "authentication rejected", 0)
    r.AddAttrs(slog.String("auth_reason", "SECRET"), slog.String("route", "GET /api/conversations/SECRET"), slog.String("rejection_reason", "SECRET"))
    safe := redactRecord(r)
    encoded, err := json.Marshal(safe.Message)
    if err != nil {
        t.Fatal(err)
    }
    if strings.Contains(string(encoded), "SECRET") {
        t.Fatal("untrusted value escaped")
    }
    count := 0
    safe.Attrs(func(_ slog.Attr) bool { count++; return true })
    if count != 0 {
        t.Fatal("untrusted diagnostic attributes retained")
    }
}
