package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const requestRecordLimit = 128

type requestLogKey struct{}

func requestLogger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(requestLogKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

type retainedRecord struct {
	record slog.Record
	output slog.Handler
}

// Request work must finish (including joining goroutines) before finalization.
// Late work is a lifecycle defect: emit a correlated diagnostic immediately,
// rather than retaining data after completion or silently losing failures.
type requestRecorder struct {
	mu       sync.Mutex
	output   slog.Handler
	records  []retainedRecord
	failures []string
	dropped  int
	closed   bool
}

type requestHandler struct {
	recorder *requestRecorder
	output   slog.Handler
}

func (h *requestHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelDebug
}
func (h *requestHandler) Handle(ctx context.Context, record slog.Record) error {
	record = redactRecord(record)
	f := h.recorder
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		late := slog.NewRecord(time.Now(), slog.LevelError, "late request event", 0)
		late.AddAttrs(slog.String("failure_reason", "late_work"))
		// Direct Handle deliberately bypasses the sink's normal level threshold.
		return f.output.Handle(context.WithoutCancel(ctx), late)
	}
	if record.Level >= slog.LevelError {
		f.failLocked("application_error")
	}
	if len(f.records) == requestRecordLimit {
		f.dropped++
		return nil
	}
	f.records = append(f.records, retainedRecord{record.Clone(), h.output})
	return nil
}
func (h *requestHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requestHandler{recorder: h.recorder, output: h.output.WithAttrs(redactAttrs(attrs))}
}
func (h *requestHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	// Group names, like operation values, must be fixed application vocabulary.
	// Preserve slog scoping, including arbitrary static names and empty groups.
	return &requestHandler{recorder: h.recorder, output: h.output.WithGroup(boundedDiagnostic(name))}
}
func (f *requestRecorder) fail(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failLocked(reason)
}
func (f *requestRecorder) failLocked(reason string) {
	for _, existing := range f.failures {
		if existing == reason {
			return
		}
	}
	f.failures = append(f.failures, reason)
}
func (f *requestRecorder) finish(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	defer func() { f.records = nil; f.failures = nil }()
	if len(f.failures) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, entry := range f.records {
		// JSON/Text handlers filter in Enabled, not Handle: replay debug unchanged.
		_ = entry.output.Handle(ctx, entry.record)
	}
	summary := slog.NewRecord(time.Now(), slog.LevelError, "request failed", 0)
	summary.AddAttrs(slog.String("failure_reason", strings.Join(f.failures, ",")), slog.Int("dropped_records", f.dropped))
	_ = f.output.Handle(ctx, summary)
}

// Retain only bounded, structured operational diagnostics. In particular,
// resolve LogValuers now and discard unknown keys/messages before buffering.
// Add new vocabulary deliberately alongside redaction tests.
func redactRecord(record slog.Record) slog.Record {
	message := record.Message
	switch message {
	case "request started", "request completed", "operation failed", "inference started", "inference completed", "reply persisted":
	default:
		message = "diagnostic event"
	}
	safe := slog.NewRecord(record.Time, record.Level, message, record.PC)
	attrs := make([]slog.Attr, 0, min(record.NumAttrs(), 16))
	record.Attrs(func(attr slog.Attr) bool { attrs = append(attrs, attr); return len(attrs) < 16 })
	safe.AddAttrs(redactAttrs(attrs)...)
	return safe
}
func redactAttrs(attrs []slog.Attr) []slog.Attr {
	safe := make([]slog.Attr, 0, min(len(attrs), 16))
	for _, attr := range attrs[:min(len(attrs), 16)] {
		value := attr.Value.Resolve()
		if value.Kind() == slog.KindGroup {
			safe = append(safe, slog.Attr{Key: boundedDiagnostic(attr.Key), Value: slog.GroupValue(redactAttrs(value.Group())...)})
			continue
		}
		switch attr.Key {
		case "operation", "error_kind", "filesystem_operation", "http_operation", "cause_type":
			if value.Kind() == slog.KindString {
				safe = append(safe, slog.String(attr.Key, boundedDiagnostic(value.String())))
			}
		case "status_code", "errno":
			if value.Kind() == slog.KindInt64 {
				safe = append(safe, slog.Int64(attr.Key, value.Int64()))
			}
		case "timeout":
			if value.Kind() == slog.KindBool {
				safe = append(safe, slog.Bool(attr.Key, value.Bool()))
			}
		}
	}
	return safe
}
func boundedDiagnostic(s string) string {
	if len(s) > 128 {
		return s[:128]
	}
	return s
}

// Unwrap keeps ResponseController deadlines available. FlushError observes
// streaming failures; Write observes failures after a successful status too.
type recordedResponse struct {
	http.ResponseWriter
	recorder *requestRecorder
	status   int
}

func (w *recordedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *recordedResponse) WriteHeader(status int) {
	if w.status == 0 && (status >= 200 || status == http.StatusSwitchingProtocols) {
		w.status = status
		if status >= 400 {
			w.recorder.fail("http_error")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *recordedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	if err != nil || n != len(data) {
		w.recorder.fail("response_write")
	}
	return n, err
}
func (w *recordedResponse) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.recorder.fail("response_flush")
	}
	return err
}
func (w *recordedResponse) Flush() { _ = w.FlushError() }

func recordRequests(next http.Handler, output slog.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate correlation locally; never trust a request header or log a URL.
		output := output.WithAttrs([]slog.Attr{slog.String("request_id", opaqueID())})
		recorder := &requestRecorder{output: output}
		logger := slog.New(&requestHandler{recorder: recorder, output: output})
		ctx := context.WithValue(r.Context(), requestLogKey{}, logger)
		response := &recordedResponse{ResponseWriter: w, recorder: recorder}
		defer func() {
			panicValue := recover()
			if panicValue != nil {
				recorder.fail("panic")
			}
			if err := ctx.Err(); err != nil {
				reason := "cancelled"
				if errors.Is(err, context.DeadlineExceeded) {
					reason = "deadline_exceeded"
				}
				recorder.fail(reason)
			}
			status := response.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.DebugContext(ctx, "request completed", "status_code", status)
			recorder.finish(ctx)
			// Preserve net/http's recovery policy, including ErrAbortHandler.
			if panicValue != nil {
				panic(panicValue)
			}
		}()
		logger.DebugContext(ctx, "request started")
		next.ServeHTTP(response, r.WithContext(ctx))
	})
}

var _ http.Flusher = (*recordedResponse)(nil)
