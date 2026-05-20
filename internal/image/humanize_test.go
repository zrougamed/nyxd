package image

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestHumanizePullError(t *testing.T) {
	t.Parallel()
	if got := HumanizePullError(context.DeadlineExceeded); got == "" || got == context.DeadlineExceeded.Error() {
		t.Fatalf("expected human message for deadline, got %q", got)
	}
	if got := HumanizePullError(errors.New("stream blob: context deadline exceeded (Client.Timeout while reading body)")); got == "" {
		t.Fatal("expected non-empty")
	}
	if got := HumanizePullError(errors.New("digest mismatch for sha256:abc")); !strings.Contains(got, "digest") {
		t.Fatalf("expected digest mention, got %q", got)
	}
	if got := HumanizePullError(errors.New(`invalid character '<' looking for beginning of value`)); strings.Contains(got, "invalid character") {
		t.Fatalf("expected humanized JSON/HTML hint, got %q", got)
	}
	if got := HumanizePullError(errors.New("registry manifest 1.36: body was HTML/XML (proxy), not JSON: x")); !strings.Contains(got, "web page") && !strings.Contains(got, "proxy") {
		t.Fatalf("expected HTML hint, got %q", got)
	}
	wrapped := fmt.Errorf(`service "alpha" pull "docker.io/library/busybox:1.36": pull manifest docker.io/library/busybox:1.36: %w`,
		errors.New(`invalid character '<' looking for beginning of value`))
	if got := HumanizeComposeBuildError(wrapped); strings.Contains(got, "invalid character") {
		t.Fatalf("compose build pull should humanize, got %q", got)
	}
}