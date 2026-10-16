package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/oauth2"
)

func TestDiagnosticsDoNotLogSensitiveErrorText(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	secret := "PRIVATE-TOKEN-AND-PROMPT"
	for _, err := range []error{
		fmt.Errorf("context %s: %w", secret, &os.PathError{Op: "open", Path: secret, Err: syscall.ENOSPC}),
		&url.Error{Op: "Post", URL: "https://user:" + secret + "@issuer.example/token?code=" + secret, Err: errors.New(secret)},
		&oauth2.RetrieveError{Response: &http.Response{StatusCode: 401}, Body: []byte(secret), ErrorCode: secret},
		&upstreamStatusError{status: 503},
	} {
		logFailure(context.Background(), "test_operation", err)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("sensitive error text entered logs")
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 4 {
		t.Fatal("missing diagnostics")
	}
	var disk, token, upstream map[string]any
	for _, item := range []struct {
		line string
		dst  *map[string]any
	}{{lines[0], &disk}, {lines[2], &token}, {lines[3], &upstream}} {
		if err := json.Unmarshal([]byte(item.line), item.dst); err != nil {
			t.Fatal(err)
		}
	}
	if disk["operation"] != "test_operation" || disk["error_kind"] != "disk_full" || disk["filesystem_operation"] != "open" {
		t.Fatal("filesystem cause not recorded")
	}
	if token["status_code"] != float64(401) || upstream["status_code"] != float64(503) {
		t.Fatal("HTTP status not recorded")
	}
}
