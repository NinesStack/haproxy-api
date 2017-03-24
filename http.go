package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/Nitro/sidecar/catalog"
	"github.com/Nitro/sidecar/service"
	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

type ApiErrors struct {
	Errors []string `json:"errors"`
}

type ApiStatus struct {
	Message        string           `json:"message"`
	LastChanged    time.Time        `json:"last_changed"`
	ServiceChanged *service.Service `json:"last_service_changed"`
}

// The health check endpoint. Tells us if HAproxy is running and has
// been properly configured. Since this is critical infrastructure this
// helps make sure a host is not "down" by havign the proxy down.
func healthHandler(response http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	response.Header().Set("Content-Type", "application/json")

	errors := make([]string, 0)

	// Do we have an HAproxy instance running?
	err := run("test -f " + proxy.PidFile + " && ps aux `cat " + proxy.PidFile + "`")
	if err != nil {
		errors = append(errors, "No HAproxy running!")
	}

	stateLock.Lock()
	defer stateLock.Unlock()

	// We were able to write out the template and reload the last time we tried?
	if updateSuccess == false {
		errors = append(errors, "Last attempted HAproxy config write failed!")
	}

	// Umm, crap, something went wrong.
	if errors != nil && len(errors) != 0 {
		message, _ := json.Marshal(ApiErrors{errors})
		response.WriteHeader(http.StatusInternalServerError)
		response.Write(message)
		return
	}

	var lastChanged time.Time
	if currentState != nil {
		lastChanged = currentState.LastChanged
	}

	message, _ := json.Marshal(ApiStatus{
		Message:        "Healthy!",
		LastChanged:    lastChanged,
		ServiceChanged: lastSvcChanged,
	})

	response.Write(message)
}

// Returns the currently stored state as a JSON blob
func stateHandler(response http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	response.Header().Set("Content-Type", "application/json")

	if currentState == nil {
		message, _ := json.Marshal(ApiErrors{[]string{"No currently stored state"}})
		response.WriteHeader(http.StatusInternalServerError)
		response.Write(message)
		return
	}

	response.Write(currentState.Encode())
}

// Receives POSTed state updates from a Sidecar instance
func updateHandler(response http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	response.Header().Set("Content-Type", "application/json")

	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		message, _ := json.Marshal(ApiErrors{[]string{err.Error()}})
		response.WriteHeader(http.StatusInternalServerError)
		response.Write(message)
		return
	}

	var evt catalog.StateChangedEvent
	err = json.Unmarshal(data, &evt)
	if err != nil {
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	stateLock.Lock()
	if currentState == nil || currentState.LastChanged.Before(evt.State.LastChanged) {
		currentState = &evt.State
		lastSvcChanged = &evt.ChangeEvent.Service
		maybeNotify(evt.ChangeEvent.PreviousStatus, evt.ChangeEvent.Service.Status)
	}
	stateLock.Unlock()
}

// Start the HTTP server and begin handling requests. This is a
// blocking call.
func serveHttp(listenIp string, listenPort int) {
	listenStr := fmt.Sprintf("%s:%d", listenIp, listenPort)

	log.Infof("Starting up on %s", listenStr)
	router := mux.NewRouter()

	router.HandleFunc("/update", updateHandler).Methods("POST")
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/state", stateHandler).Methods("GET")
	http.Handle("/", handlers.LoggingHandler(os.Stdout, router))

	err := http.ListenAndServe(listenStr, nil)
	if err != nil {
		log.Fatalf("Can't start http server: %s", err.Error())
	}
}
