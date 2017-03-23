package main

import (
	"net/http"
	"time"

	"github.com/Nitro/sidecar/catalog"
	"github.com/Nitro/sidecar/service"
	log "github.com/Sirupsen/logrus"
	"github.com/relistan/go-director"
)

// A SidecarWatcher attaches to the /watch endpoint on a Sidecar instance and
// sends notifications when something has changed in the remote state.
//
// haproxy-api uses this when in Follow mode.

const (
	CONNECTION_REFRESH_TIME = 3 * time.Minute
)

// Heavily modified from @bparli's Traefik provider for Sidecar:
// https://github.com/Nitro/traefik/blob/master/provider/sidecar.go

type SidecarWatcher struct {
	RefreshConn time.Duration // How often to refresh the connection to Sidecar backend
	Client      *http.Client
	transport   *http.Transport
	notifyChan  chan struct{}
	looper      director.Looper
	timer       *time.Timer
	url         string
}

// Return a new, fully configured SidecarWatcher
func NewSidecarWatcher(url string, looper director.Looper, notifyChan chan struct{}) *SidecarWatcher {
	tr := &http.Transport{ResponseHeaderTimeout: 0}

	w := &SidecarWatcher{
		RefreshConn: CONNECTION_REFRESH_TIME,
		Client:     &http.Client{Timeout: 0, Transport: tr},
		looper:     looper,
		transport:  tr,
		notifyChan: notifyChan,
		timer:      time.NewTimer(CONNECTION_REFRESH_TIME),
		url:        url,
	}

	log.Infof("Using Sidecar connection refresh interval: %s", w.RefreshConn.String())

	return w
}

// onChange is a callback triggered by changed from the Sidecar /watch
// endpoint. We don't need the data it sends, but the callback function
// is required to be of this signature.
func (w *SidecarWatcher) onChange(state map[string][]*service.Service, err error) error {
	// If something went wrong, we bail, don't reload HAproxy,
	// and let the connection time out
	if err != nil {
		log.Errorf("Got error from stream parser: %s", err.Error())
		return nil
	}

	w.notify()

	// Stop and reset the timer
	if !w.timer.Stop() {
		<-w.timer.C
	}
	w.resetTimer()

	return nil
}

// Utility method to rest the timer
func (w *SidecarWatcher) resetTimer() {
	w.timer.Reset(w.RefreshConn)
}

// Utility method to send the right data on the notifyChan
func (w *SidecarWatcher) notify() {
	w.notifyChan <- struct{}{}
}

// Follow() will attach to the /watch endpoint on a Sidecar instance and
// send notifications on the notifyChan when something has changed on the
// remote host. It uses a timer to guarantee that we get a refresh on the
// open connection every RefreshConn so that we don't end up being
// orphaned.
func (w *SidecarWatcher) Follow() {
	var err error
	var resp *http.Response
	var req *http.Request

	w.looper.Loop(func() error {
		req, err = http.NewRequest("GET", w.url, nil)
		if err != nil {
			log.Errorf("Error creating http request to Sidecar: %s, Error: %s", w.url, err)
			return nil
		}

		resp, err = w.Client.Do(req)
		if err != nil {
			log.Errorf("Error connecting to Sidecar: %s, Error: %s", w.url, err)
			time.Sleep(5 * time.Second)
			return nil
		}

		// DecodeStream will trigger the onChange callback on each event
		go catalog.DecodeStream(resp.Body, w.onChange)

		// Block waiting on the timer to expire
		<-w.timer.C
		w.resetTimer()
		w.transport.CancelRequest(req)
		return nil
	})
}
