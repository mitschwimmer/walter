package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"

	"golang.org/x/oauth2"
)

// Keep diagnostic structure, never arbitrary Error() strings: upstream errors
// can contain response bodies, URLs, credentials or conversation text.
func logFailure(ctx context.Context, operation string, err error) {
	kind := "internal"
	switch {
	case errors.Is(err, context.Canceled):
		kind = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		kind = "deadline_exceeded"
	case errors.Is(err, os.ErrPermission):
		kind = "permission_denied"
	case errors.Is(err, os.ErrNotExist):
		kind = "not_found"
	case errors.Is(err, syscall.ENOSPC):
		kind = "disk_full"
	}
	attrs := []any{"operation", operation, "error_kind", kind}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		attrs = append(attrs, "filesystem_operation", pathErr.Op)
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		attrs = append(attrs, "errno", int(errno))
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		attrs = append(attrs, "http_operation", urlErr.Op)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		attrs = append(attrs, "timeout", netErr.Timeout())
	}
	var upstream *upstreamStatusError
	if errors.As(err, &upstream) {
		attrs = append(attrs, "status_code", upstream.status)
	}
	var tokenErr *oauth2.RetrieveError
	if errors.As(err, &tokenErr) && tokenErr.Response != nil {
		attrs = append(attrs, "status_code", tokenErr.Response.StatusCode)
	}
	cause := err
	for errors.Unwrap(cause) != nil {
		cause = errors.Unwrap(cause)
	}
	attrs = append(attrs, "cause_type", fmt.Sprintf("%T", cause))
	requestLogger(ctx).ErrorContext(ctx, "operation failed", attrs...)
}
