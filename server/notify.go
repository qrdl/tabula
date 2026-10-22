package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
)

// Notifier tells the device to refresh when the image it shows is out of date.
type Notifier struct {
	store  *Store
	frame  func(context.Context) (*Frame, error)
	port   string // the device's HTTP port
	client *http.Client
	kick   chan struct{}
	// lastTag is the ETag of the image the device was last told about.
	// Only the Run goroutine uses it.
	lastTag string
}

func NewNotifier(store *Store, frame func(context.Context) (*Frame, error)) *Notifier {
	return &Notifier{
		store:  store,
		frame:  frame,
		port:   "80",
		client: &http.Client{Timeout: 5 * time.Second},
		kick:   make(chan struct{}, 1),
	}
}

// Changed schedules a check. It never blocks; changes made while a check is
// pending are covered by that check.
func (n *Notifier) Changed() {
	select {
	case n.kick <- struct{}{}:
	default:
	}
}

// Run handles checks until ctx is done. It first asks the device to refresh, since the
// image may have changed while the server was down (a new config, for example);
// if it hasn't, the device's download gets a 304 and nothing is redrawn.
func (n *Notifier) Run(ctx context.Context) {
	n.start(ctx)
	n.notify(ctx)
	n.loop(ctx)
}

func (n *Notifier) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.kick:
			n.check(ctx)
		}
	}
}

// start records the current image as the one the device has; it fetches the image itself when it boots.
func (n *Notifier) start(ctx context.Context) {
	tag, err := n.currentTag(ctx)
	if err != nil {
		log.Printf("notify: render: %v", err)
	}
	n.lastTag = tag
}

func (n *Notifier) currentTag(ctx context.Context) (string, error) {
	f, err := n.frame(ctx)
	if err != nil {
		return "", err
	}
	return ETag(f.PanelData()), nil
}

// check sends a refresh request to the device if the image has changed since the last one.
func (n *Notifier) check(ctx context.Context) {
	tag, err := n.currentTag(ctx)
	if err != nil {
		log.Printf("notify: render: %v", err)
		return
	}
	if tag == n.lastTag {
		return
	}
	n.lastTag = tag
	n.notify(ctx)
}

// notify sends the device a refresh request.
func (n *Notifier) notify(ctx context.Context) {
	addr, err := n.store.DeviceAddr(ctx)
	if err != nil {
		log.Printf("notify: device address: %v", err)
		return
	}
	if addr == "" {
		log.Printf("notify: the device hasn't connected yet")
		return
	}
	url := "http://" + net.JoinHostPort(addr, n.port) + "/refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("notify: %s: %s", url, resp.Status)
		return
	}
	log.Printf("notify: asked device %s to refresh", addr)
}
