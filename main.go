package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/newrelic/sidecar/catalog"
	"github.com/newrelic/sidecar/haproxy"
	"github.com/newrelic/sidecar/service"
	"gopkg.in/alecthomas/kingpin.v1"
)

const (
	RELOAD_BUFFER    = 256
	RELOAD_HOLD_DOWN = 5 * time.Second // Reload at worst every 5 seconds
)

var (
	proxy          *haproxy.HAproxy
	stateLock      sync.Mutex
	reloadChan     chan time.Time
	currentState   *catalog.ServicesState
	lastSvcChanged *service.Service
	updateSuccess  bool
	version        string = "Version not set!"
)

type CliOpts struct {
	ConfigFile *string
}

type ApiErrors struct {
	Errors []string `json:"errors"`
}

type ApiStatus struct {
	Message        string           `json:"message"`
	LastChanged    time.Time        `json:"last_changed"`
	ServiceChanged *service.Service `json:"last_service_changed"`
	Version	       string		`json:"version"`
}

func exitWithError(err error, message string) {
	if err != nil {
		log.Fatal("%s: %s", message, err.Error())
	}
}

func parseCommandLine() *CliOpts {
	var opts CliOpts
	opts.ConfigFile = kingpin.Flag("config-file", "The config file to use").Short('f').Default("haproxy-api.toml").String()
	kingpin.Parse()
	return &opts
}

func run(command string) error {
	cmd := exec.Command("/bin/bash", "-c", command)
	err := cmd.Run()
	if err != nil {
		log.Errorf("Error running '%s': %s", command, err.Error())
	}

	return err
}

// The health check endpoint. Tells us if HAproxy is running and has
// been properly configured. Since this is critical infrastructure this
// helps make sure a host is not "down" by having the proxy down.
func healthHandler(response http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	response.Header().Set("Content-Type", "application/json")

	errors := make([]string, 0)

	// Do we have an HAproxy instance running?
	err := run("test -f " + proxy.PidFile + " && ps aux `cat " + proxy.PidFile + "`")
	if err != nil {
		errors = append(errors, version)
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
		Version:	version,
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

// Check all the state transitions and only update HAproxy when a change
// will affect service availability.
func maybeNotify(oldState int, newState int) {
	updated := false

	log.Debugf("Checking event. OldStatus: %s NewStatus: %s",
		service.StatusString(oldState), service.StatusString(newState),
	)

	// Compare old and new states to find significant changes only
	switch newState {
	case service.ALIVE:
		updated = true
		enqueueUpdate()
	case service.TOMBSTONE:
		updated = true
		enqueueUpdate()
	case service.UNKNOWN:
		if oldState == service.ALIVE {
			updated = true
			enqueueUpdate()
		}
	case service.UNHEALTHY:
		if oldState == service.ALIVE {
			updated = true
			enqueueUpdate()
		}
	default:
		log.Errorf("Got unknown service change status: %d", newState)
	}

	if !updated {
		log.Debugf("Skipped HAproxy update due to state machine check")
	}
}

// Loop forever, processing updates to the state.
func processUpdates() {
	for {
		// Batch up to RELOAD_BUFFER number updates into a
		// single update.
		first := <-reloadChan
		pending := len(reloadChan)

		writeAndReload(currentState)

		// We just flushed the most recent state, dump all the
		// pending items up to that point.
		var reload time.Time
		for i := 0; i < pending; i++ {
			reload = <-reloadChan
		}

		if first.Before(reload) {
			log.Infof("Skipped %d messages between %s and %s", pending, first, reload)
		}

		// Don't notify more frequently than every RELOAD_HOLD_DOWN period. When a
		// deployment rolls across the cluster it can trigger a bunch of groupable
		// updates.
		log.Debug("Holding down...")
		time.Sleep(RELOAD_HOLD_DOWN)
	}
}

// Write out the HAproxy config and reload the instance
func writeAndReload(state *catalog.ServicesState) {
	log.Info("Updating HAproxy")
	err := proxy.WriteAndReload(state)
	updateSuccess = (err == nil)
}

// Process and update by queueing a writeAndReload().
func enqueueUpdate() {
	reloadChan <- time.Now().UTC()
}

// Used to fetch the current state from a Sidecar endpoint, usually
// on startup of this process, when the currentState is empty.
func fetchState(url string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	state, err := catalog.Decode(bytes)
	if err != nil {
		return err
	}

	currentState = state
	writeAndReload(state)

	return nil
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

func main() {
	opts := parseCommandLine()
	config := parseConfig(*opts.ConfigFile)

	proxy = config.HAproxy

	reloadChan = make(chan time.Time, RELOAD_BUFFER)

	log.Info("Fetching initial state on startup...")
	err := fetchState(config.Sidecar.StateUrl)
	if err != nil {
		log.Errorf("Failed to fetch state from '%s'... continuing in hopes someone will post it", config.Sidecar.StateUrl)
	} else {
		log.Info("Successfully retrieved state")
	}

	go processUpdates()

	serveHttp(config.HAproxyApi.BindIP, config.HAproxyApi.BindPort)

	close(reloadChan)
}
