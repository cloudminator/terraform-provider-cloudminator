package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "cm_test", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	c.RetryWait = time.Millisecond
	return c
}

func TestNewRejectsRelativeEndpoint(t *testing.T) {
	if _, err := New("platform.example.com", "k", "v"); err == nil {
		t.Fatal("expected an error for an endpoint without scheme")
	}
}

func TestHeadersAndDecode(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer cm_test" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "terraform-provider-cloudminator/1.2.3" {
			t.Errorf("User-Agent = %q", got)
		}
		_, _ = io.WriteString(w, `{"metadata":{"id":"vm-1","name":"web"},"spec":{"image":"debian-13","size":"2c-4g"},"status":{"phase":"Running","observed":{"address":"10.0.0.5","version":3}}}`)
	})
	env, err := GetResource[VMSpec](context.Background(), c, "acme", "prod", KindVMs, "vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if env.Metadata.ID != "vm-1" || env.Spec.Size != "2c-4g" || env.Status.ObservedString("address") != "10.0.0.5" || env.Status.ObservedInt("version") != 3 {
		t.Fatalf("decoded %+v", env)
	}
}

func TestAPIErrorAndNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"vms not found"}`)
	})
	_, err := GetResource[VMSpec](context.Background(), c, "acme", "prod", KindVMs, "nope")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	if !strings.Contains(err.Error(), "vms not found") {
		t.Fatalf("error lacks API message: %v", err)
	}
}

func TestGetRetriesTransientFailures(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"regions":[{"id":"eu-central-1"}]}`)
	})
	regions, err := c.ListRegions(context.Background())
	if err != nil || len(regions) != 1 || calls != 3 {
		t.Fatalf("regions=%v err=%v calls=%d", regions, err, calls)
	}
}

func TestPlainPostIsNotRetried(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := c.Post(context.Background(), "/x", map[string]string{}, nil); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("POST without an idempotency key was sent %d times", calls)
	}
}

func TestCreateReusesKeyAndBodyAcrossRetries(t *testing.T) {
	var keys, bodies []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		bodies = append(bodies, string(b))
		switch len(keys) {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
		case 2:
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"a request with this Idempotency-Key is in progress"}`)
		default:
			w.Header().Set("Idempotent-Replayed", "true")
			_, _ = io.WriteString(w, `{"metadata":{"id":"vm-1"},"spec":{},"status":{"phase":"Creating"}}`)
		}
	})
	env, err := CreateResource[VMSpec](context.Background(), c, "acme", "prod", KindVMs,
		CreateRequest[VMSpec]{Name: "web", Spec: VMSpec{Image: "debian-13", Size: "2c-4g"}})
	if err != nil {
		t.Fatal(err)
	}
	if env.Metadata.ID != "vm-1" || len(keys) != 3 {
		t.Fatalf("id=%q attempts=%d", env.Metadata.ID, len(keys))
	}
	if keys[0] == "" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("Idempotency-Key not stable across retries: %v", keys)
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Fatal("create body bytes changed across retries")
	}
}

func TestCreateKeysDifferPerCall(t *testing.T) {
	var keys []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = io.WriteString(w, `{"metadata":{"id":"b"}}`)
	})
	for range 2 {
		if _, err := CreateResource[BucketSpec](context.Background(), c, "o", "p", KindBuckets, CreateRequest[BucketSpec]{Name: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	if keys[0] == keys[1] {
		t.Fatal("two Create calls shared an Idempotency-Key")
	}
}

func TestRealConflictIsNotRetried(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"already exists"}`)
	})
	_, err := CreateResource[BucketSpec](context.Background(), c, "o", "p", KindBuckets, CreateRequest[BucketSpec]{Name: "b"})
	if !IsConflict(err) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestWaitForPhase(t *testing.T) {
	PollInterval = time.Millisecond
	phases := []string{"Pending", "Creating", "Running"}
	i := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		p := phases[min(i, len(phases)-1)]
		i++
		_, _ = io.WriteString(w, `{"metadata":{"id":"x"},"status":{"phase":"`+p+`"}}`)
	})
	env, err := WaitForPhase[VPCSpec](context.Background(), c, "o", "p", KindVPCs, "x")
	if err != nil || env.Status.Phase != PhaseRunning {
		t.Fatalf("phase=%q err=%v", env.Status.Phase, err)
	}
}

func TestWaitForPhaseError(t *testing.T) {
	PollInterval = time.Millisecond
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"metadata":{"id":"x"},"status":{"phase":"Error","message":"no capacity"}}`)
	})
	_, err := WaitForPhase[VMSpec](context.Background(), c, "o", "p", KindVMs, "x")
	if err == nil || !strings.Contains(err.Error(), "no capacity") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitForPhaseTimeout(t *testing.T) {
	PollInterval = time.Millisecond
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"metadata":{"id":"x"},"status":{"phase":"Creating"}}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := WaitForPhase[VMSpec](ctx, c, "o", "p", KindVMs, "x"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitForDeleted(t *testing.T) {
	PollInterval = time.Millisecond
	n := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		if n < 3 {
			_, _ = io.WriteString(w, `{"metadata":{"id":"x"},"status":{"phase":"Deleting"}}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	if err := WaitForDeleted[VMSpec](context.Background(), c, "o", "p", KindVMs, "x"); err != nil {
		t.Fatal(err)
	}
}
