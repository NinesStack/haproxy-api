package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Nitro/sidecar/catalog"
	"github.com/Nitro/sidecar/receiver"
	"github.com/Nitro/sidecar/service"
	. "github.com/smartystreets/goconvey/convey"
)

func Test_stateHandler(t *testing.T) {
	Convey("stateHandler()", t, func() {
		rcvr := &receiver.Receiver{
			ReloadChan: make(chan time.Time, 10),
		}
		rcvr.OnUpdate = func(state *catalog.ServicesState) { rcvr.EnqueueUpdate() }

		hostname := "chaucer"
		state := catalog.NewServicesState()
		state.Servers[hostname] = catalog.NewServer(hostname)

		baseTime := time.Now().UTC()

		svcId := "deadbeef123"
		svcId2 := "deadbeef456"

		svc := service.Service{
			ID:       svcId,
			Name:     "bocaccio",
			Image:    "101deadbeef",
			Created:  baseTime,
			Hostname: hostname,
			Updated:  baseTime,
			Status:   service.ALIVE,
		}

		svc2 := service.Service{
			ID:       svcId2,
			Name:     "shakespeare",
			Image:    "202deadbeef",
			Created:  baseTime,
			Hostname: hostname,
			Updated:  baseTime,
			Status:   service.ALIVE,
		}

		state.AddServiceEntry(svc)
		state.AddServiceEntry(svc2)

		req := httptest.NewRequest("GET", "/state", nil)
		recorder := httptest.NewRecorder()

		// Assign to the :( global
		rcvr.CurrentState = state

		Convey("sends the complete state, properly encoded", func() {
			stateHandler(recorder, req, rcvr)

			resp := recorder.Result()
			bodyBytes, _ := ioutil.ReadAll(resp.Body)

			var received *catalog.ServicesState
			json.Unmarshal(bodyBytes, &received)

			So(received.HasServer("chaucer"), ShouldBeTrue)
			So(received.Servers["chaucer"].HasService(svcId), ShouldBeTrue)
			So(received.Servers["chaucer"].HasService(svcId2), ShouldBeTrue)
		})

		Convey("sends the right content-type", func() {
			stateHandler(recorder, req, rcvr)

			resp := recorder.Result()
			So(resp.Header.Get("Content-Type"), ShouldEqual, "application/json")
		})
	})
}
