package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func diagnosticRecords(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
}
func diagnosticSink(output *bytes.Buffer) slog.Handler {
	// Successful replay must include debug even when the sink's threshold is Error.
	return slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelError})
}

func TestRequestRecorderLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		handle       func(http.ResponseWriter, *http.Request)
	}{
		{"success", "", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }},
		{"client error", "http_error", func(w http.ResponseWriter, r *http.Request) { problem(w, 403, "denied") }},
		{"server error", "http_error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"handled dependency", "application_error", func(w http.ResponseWriter, r *http.Request) {
			logFailure(r.Context(), "context_capacity", errors.New("SECRET"))
			w.WriteHeader(200)
		}},
		{"encoding error", "response_encoding", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, make(chan int))
		}},
		{"cancelled", "cancelled", func(w http.ResponseWriter, r *http.Request) {}},
		{"deadline", "deadline_exceeded", func(w http.ResponseWriter, r *http.Request) {}},
		{"panic", "panic", func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) }},
		{"inner recovery", "application_error", func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recover() != nil {
					logFailure(r.Context(), "recover_request", errors.New("SECRET"))
				}
			}()
			panic("SECRET")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := recordRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestLogger(r.Context()).DebugContext(r.Context(), "inference started", "operation", "test_request", "prompt", "SECRET")
				if output.Len() != 0 {
					t.Error("emitted before response completed")
				}
				tc.handle(w, r)
			}), diagnosticSink(&output))
			ctx := context.Background()
			if tc.name == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if tc.name == "deadline" {
				expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				ctx = expired
			}
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/SECRET?token=SECRET", nil).WithContext(ctx))
			}()
			if tc.name == "panic" && recovered != http.ErrAbortHandler {
				t.Fatalf("panic policy changed: %v", recovered)
			}
			if tc.name != "panic" && recovered != nil {
				t.Fatal(recovered)
			}
			if tc.reason == "" {
				if output.Len() != 0 {
					t.Fatal("successful request logged", output.String())
				}
				return
			}
			records := diagnosticRecords(t, &output)
			summaries := 0
			id := records[0]["request_id"]
			for _, record := range records {
				if record["request_id"] != id || id == "" {
					t.Fatal("lost correlation", records)
				}
				if record["msg"] == "request failed" {
					summaries++
					if !strings.Contains(record["failure_reason"].(string), tc.reason) {
						t.Fatal(record)
					}
				}
			}
			if summaries != 1 || records[0]["level"] != "DEBUG" || records[len(records)-2]["msg"] != "request completed" {
				t.Fatal("incomplete/duplicate replay", records)
			}
			if strings.Contains(output.String(), "SECRET") {
				t.Fatal("sensitive data logged")
			}
		})
	}
}

func TestRecorderScopesOrderingOverflowAndRelease(t *testing.T) {
	var output bytes.Buffer
	sink := diagnosticSink(&output)
	f := &requestRecorder{output: sink}
	handler := &requestHandler{recorder: f, output: sink}
	logger := slog.New(handler).With("operation", "before_group", "token", "SECRET").WithGroup("inference").With("status_code", 503).WithGroup("storage")
	when := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	record := slog.NewRecord(when, slog.LevelDebug, "inference started", 0)
	record.AddAttrs(slog.String("operation", "inside_group"), slog.String("prompt", "SECRET"), slog.Group("", slog.Bool("timeout", true)), slog.Group("upstream", slog.Int("errno", 5), slog.String("token", "SECRET")))
	if err := logger.Handler().Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's record must not change the retained entry.
	record.AddAttrs(slog.String("operation", "later_mutation"))
	for i := 1; i < requestRecordLimit+5; i++ {
		slog.New(handler).Debug("inference started", "status_code", i)
	}
	// Even an error dropped by the buffer must cause a flush.
	slog.New(handler).Error("operation failed")
	f.finish(context.Background())
	f.finish(context.Background())
	records := diagnosticRecords(t, &output)
	if len(records) != requestRecordLimit+1 || records[len(records)-1]["dropped_records"] != float64(6) {
		t.Fatal("wrong overflow/finalization", len(records), records[len(records)-1])
	}
	first := records[0]
	if first["time"] != when.Format(time.RFC3339) || first["level"] != "DEBUG" || first["operation"] != "before_group" {
		t.Fatal(first)
	}
	inference := first["inference"].(map[string]any)
	if inference["status_code"] != float64(503) || inference["storage"].(map[string]any)["operation"] != "inside_group" {
		t.Fatal(first)
	}
	storage := inference["storage"].(map[string]any)
	if storage["timeout"] != true || storage["upstream"].(map[string]any)["errno"] != float64(5) {
		t.Fatal("group semantics lost", first)
	}
	if strings.Contains(output.String(), "SECRET") || strings.Contains(output.String(), "later_mutation") {
		t.Fatal("retained unsafe/mutated attributes")
	}
	if f.records != nil || f.failures != nil {
		t.Fatal("retained completed request data")
	}
	for i := 1; i < requestRecordLimit; i++ {
		if records[i]["status_code"] != float64(i) {
			t.Fatal("replay reordered", records[i])
		}
	}
	// Work using an expired logger is diagnosed without reopening the buffer.
	logger.Error("operation failed")
	records = diagnosticRecords(t, &output)
	if records[len(records)-1]["failure_reason"] != "late_work" || f.records != nil {
		t.Fatal("late work lost or retained")
	}
}

func TestConcurrentRequestRecorders(t *testing.T) {
	var output bytes.Buffer
	handler := recordRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := requestLogger(r.Context())
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Debug("inference started", "operation", r.Header.Get("Test-Operation"))
			}()
		}
		wg.Wait()
		if r.Header.Get("Test-Fail") == "yes" {
			w.WriteHeader(500)
		}
	}), diagnosticSink(&output))
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Test-Operation", fmt.Sprintf("request_%d", i))
			if i%2 == 0 {
				r.Header.Set("Test-Fail", "yes")
			}
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}()
	}
	wg.Wait()
	owners := map[string]string{}
	summaries := map[string]int{}
	counts := map[string]int{}
	for _, record := range diagnosticRecords(t, &output) {
		id := record["request_id"].(string)
		if operation, ok := record["operation"].(string); ok {
			if old := owners[id]; old != "" && old != operation {
				t.Fatal("mixed requests", old, operation)
			}
			owners[id] = operation
			counts[id]++
		}
		if record["msg"] == "request failed" {
			summaries[id]++
		}
	}
	if len(owners) != 10 {
		t.Fatal("successful requests leaked or failures lost", owners)
	}
	for id := range owners {
		if summaries[id] != 1 || counts[id] != 8 {
			t.Fatal(id, summaries[id], counts[id])
		}
	}
}

type diagnosticWriter struct {
	*httptest.ResponseRecorder
	writeError, flushError error
	short                  bool
	deadline               time.Time
	failDone               bool
}

func (w *diagnosticWriter) Write(data []byte) (int, error) {
	if w.writeError != nil || (w.failDone && bytes.Contains(data, []byte("event: done"))) {
		return 0, io.ErrClosedPipe
	}
	if w.short {
		return len(data) - 1, nil
	}
	return w.ResponseRecorder.Write(data)
}
func (w *diagnosticWriter) FlushError() error { return w.flushError }
func (w *diagnosticWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestRecorderResponseController(t *testing.T) {
	for _, mode := range []string{"write", "short", "flush", "success"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			writer := &diagnosticWriter{ResponseRecorder: httptest.NewRecorder()}
			if mode == "write" {
				writer.writeError = io.ErrClosedPipe
			}
			if mode == "short" {
				writer.short = true
			}
			if mode == "flush" {
				writer.flushError = io.ErrClosedPipe
			}
			deadline := time.Now().Add(time.Second)
			handler := recordRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil {
					t.Error(err)
				}
				w.WriteHeader(200)
				_, _ = w.Write([]byte("hello"))
				_ = http.NewResponseController(w).Flush()
			}), diagnosticSink(&output))
			handler.ServeHTTP(writer, httptest.NewRequest("GET", "/", nil))
			if !writer.deadline.Equal(deadline) {
				t.Fatal("deadline support lost")
			}
			if mode == "success" {
				if output.Len() != 0 {
					t.Fatal(output.String())
				}
				return
			}
			records := diagnosticRecords(t, &output)
			reason := "response_write"
			if mode == "flush" {
				reason = "response_flush"
			}
			if records[len(records)-1]["failure_reason"] != reason {
				t.Fatal(records)
			}
		})
	}
}

func TestFinalSSEWriteFailureIsRecordedAfterPersistence(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(diagnosticSink(&output)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	a := testApp(t)
	a.cfg.Backend = "openai"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL
	c := fixture()
	if err := a.store.Save("a", c); err != nil {
		t.Fatal(err)
	}
	session := strings.Repeat("a", 43)
	a.auth.sessions[hashToken(session)] = Session{User: "a", CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	r := httptest.NewRequest("POST", "/api/conversations/"+c.ID+"/messages", strings.NewReader(`{"content":"SECRET"}`))
	r.Header.Set("Origin", a.cfg.PublicURL)
	r.Header.Set("X-CSRF-Token", "csrf")
	r.AddCookie(&http.Cookie{Name: "__Host-walter-session", Value: session})
	writer := &diagnosticWriter{ResponseRecorder: httptest.NewRecorder(), failDone: true}
	a.routes().ServeHTTP(writer, r)
	saved, err := a.store.Get("a", c.ID)
	if err != nil || saved.Messages[1].Status != "complete" {
		t.Fatal("lost saved answer", saved, err)
	}
	if strings.Contains(writer.Body.String(), "event: done") {
		t.Fatal("delivered failed write")
	}
	records := diagnosticRecords(t, &output)
	summary := records[len(records)-1]
	if summary["msg"] != "request failed" || !strings.Contains(summary["failure_reason"].(string), "response_write") {
		t.Fatal(records)
	}
	found := false
	for _, record := range records {
		if record["operation"] == "complete_response" {
			found = true
		}
	}
	if !found || strings.Contains(output.String(), "SECRET") {
		t.Fatal("missing/redaction failure", output.String())
	}
}

func TestHandledMetadataFailureIsRecorded(t *testing.T) {
	var output bytes.Buffer
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	a := testApp(t)
	a.cfg.Endpoint = upstream.URL + "/v1/chat/completions"
	handler := recordRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.promptFits(r.Context(), fixture(), "SECRET") {
			t.Error("optional metadata blocked generation")
		}
		w.WriteHeader(200)
	}), diagnosticSink(&output))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	records := diagnosticRecords(t, &output)
	if records[1]["operation"] != "context_capacity" || records[1]["status_code"] != float64(404) || records[len(records)-1]["msg"] != "request failed" {
		t.Fatal(records)
	}
}
