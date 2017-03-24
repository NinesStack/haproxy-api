package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Nitro/sidecar/catalog"
	"github.com/Nitro/sidecar/service"
	. "github.com/SmartyStreets/goconvey/convey"
)

func Test_stateHandler(t *testing.T) {
	Convey("stateHandler()", t, func() {
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
		currentState = state

		Convey("sends the complete state, properly encoded", func() {
			stateHandler(recorder, req)

			resp := recorder.Result()
			bodyBytes, _ := ioutil.ReadAll(resp.Body)

			var received *catalog.ServicesState
			json.Unmarshal(bodyBytes, &received)

			So(received.HasServer("chaucer"), ShouldBeTrue)
			So(received.Servers["chaucer"].HasService(svcId), ShouldBeTrue)
			So(received.Servers["chaucer"].HasService(svcId2), ShouldBeTrue)
		})

		Convey("sends the right content-type", func() {
			stateHandler(recorder, req)

			resp := recorder.Result()
			So(resp.Header.Get("Content-Type"), ShouldEqual, "application/json")
		})
	})
}

func Test_updateHandler(t *testing.T) {
	Convey("updateHandler()", t, func() {
		reloadChan = make(chan time.Time, RELOAD_BUFFER)

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

		req := httptest.NewRequest("POST", "/update", nil)
		recorder := httptest.NewRecorder()

		Convey("returns an error on an invalid JSON body", func() {
			// Assign to the :( global
			currentState = state

			updateHandler(recorder, req)

			resp := recorder.Result()
			So(resp.StatusCode, ShouldEqual, 500)

			bodyBytes, _ := ioutil.ReadAll(resp.Body)
			So(string(bodyBytes), ShouldContainSubstring, "unexpected end of JSON input")
		})

		Convey("updates the state", func() {
			startTime := currentState.LastChanged
			state.LastChanged = time.Now().UTC()

			change := catalog.StateChangedEvent{
				State: *state,
				ChangeEvent: catalog.ChangeEvent{
					Service: service.Service{
						ID:      "10101010101",
						Updated: time.Now().UTC(),
						Status:  service.ALIVE,
					},
					PreviousStatus: service.TOMBSTONE,
				},
			}

			encoded, _ := json.Marshal(change)
			req := httptest.NewRequest("POST", "/update", bytes.NewBuffer(encoded))

			updateHandler(recorder, req)
			resp := recorder.Result()

			So(resp.StatusCode, ShouldEqual, 200)
			So(startTime.Before(currentState.LastChanged), ShouldBeTrue)
			So(currentState.LastChanged, ShouldResemble, state.LastChanged)
			So(lastSvcChanged, ShouldResemble, &change.ChangeEvent.Service)
			So(len(reloadChan), ShouldEqual, 1)
		})
	})
}
