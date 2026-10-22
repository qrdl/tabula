package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeDevice records the requests it gets.
func fakeDevice(t *testing.T) (host, port string, requests chan string) {
	t.Helper()
	requests = make(chan string, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return host, port, requests
}

func expectRequest(t *testing.T, requests chan string, step string) {
	t.Helper()
	select {
	case r := <-requests:
		if r != "POST /refresh" {
			t.Errorf("%s: device got %q, want POST /refresh", step, r)
		}
	default:
		t.Errorf("%s: device got no request", step)
	}
}

func expectNoRequest(t *testing.T, requests chan string, step string) {
	t.Helper()
	select {
	case r := <-requests:
		t.Errorf("%s: device got %q, want nothing", step, r)
	default:
	}
}

func TestNotifierCheck(t *testing.T) {
	ctx := context.Background()
	ta := newTestAPI(t)
	host, port, requests := fakeDevice(t)
	n := NewNotifier(ta.store, ta.api.deviceFrame)
	n.port = port
	n.start(ctx)

	ta.mustDo("POST", "/api/tasks", `{"text":"Buy milk","priority":"high"}`, 201, nil)
	n.check(ctx)
	expectNoRequest(t, requests, "device address not known yet")

	if err := ta.store.SetDeviceAddr(ctx, host); err != nil {
		t.Fatal(err)
	}
	n.check(ctx)
	expectNoRequest(t, requests, "nothing changed since the last check")

	ta.mustDo("POST", "/api/tasks", `{"text":"Call mum","priority":"low"}`, 201, nil)
	n.check(ctx)
	expectRequest(t, requests, "task added")

	ta.mustDo("POST", "/api/categories", `{"name":"Home","icon":"home"}`, 201, nil)
	n.check(ctx)
	expectNoRequest(t, requests, "category added (not shown until a task uses it)")

	ta.mustDo("POST", "/api/tasks/2/close", ``, 200, nil)
	n.check(ctx)
	expectRequest(t, requests, "task closed")

	ta.mustDo("DELETE", "/api/tasks/2", ``, 204, nil)
	n.check(ctx)
	expectNoRequest(t, requests, "closed task deleted")
}

func TestNotifierRunRefreshesOnStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ta := newTestAPI(t)
	host, port, requests := fakeDevice(t)
	if err := ta.store.SetDeviceAddr(ctx, host); err != nil {
		t.Fatal(err)
	}
	n := NewNotifier(ta.store, ta.api.deviceFrame)
	n.port = port
	go n.Run(ctx)
	select {
	case r := <-requests:
		if r != "POST /refresh" {
			t.Errorf("device got %q, want POST /refresh", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("device got no request on start")
	}
}

func TestNotifierRunCoalesces(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ta := newTestAPI(t)
	host, port, requests := fakeDevice(t)
	if err := ta.store.SetDeviceAddr(ctx, host); err != nil {
		t.Fatal(err)
	}
	n := NewNotifier(ta.store, ta.api.deviceFrame)
	n.port = port
	n.start(ctx)
	go n.loop(ctx)

	ta.mustDo("POST", "/api/tasks", `{"text":"Buy milk","priority":"high"}`, 201, nil)
	for range 3 {
		n.Changed()
	}
	select {
	case <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("device got no request")
	}
	time.Sleep(200 * time.Millisecond)
	expectNoRequest(t, requests, "repeated Changed calls for one change")
}
