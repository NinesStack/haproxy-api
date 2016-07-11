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
	"gopkg.in/alecthomas/kingpin.v1"
)

const (
	RELOAD_BUFFER = 256
	// A new service usually comes in as three events.
	// By 5 seconds it's usually alive.
	RELOAD_HOLD_DOWN = 5 * time.Second
)

var (
	proxy         *haproxy.HAproxy
	stateLock     sync.Mutex
	reloadChan    chan time.Time
	currentState  *catalog.ServicesState
	updateSuccess bool
)

type CliOpts struct {
	ConfigFile *string
}

type ApiErrors struct {
	Errors []string `json:"errors"`
}

type ApiStatus struct {
	Message     string `json:"message"`
	LastChanged string `json:"last_changed"`
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

func healthHandler(response http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	response.Header().Set("Content-Type", "application/json")

	errors := make([]string, 0)

	err := run("test -f " + proxy.PidFile + " && ps aux `cat " + proxy.PidFile + "`")
	if err != nil {
		errors = append(errors, "No HAproxy running!")
	}

	stateLock.Lock()
	defer stateLock.Unlock()

	if updateSuccess == false {
		errors = append(errors, "Last attempted HAproxy config write failed!")
	}

	if len(errors) != 0 {
		message, _ := json.Marshal(ApiErrors{errors})
		response.WriteHeader(http.StatusInternalServerError)
		response.Write(message)
		return
	}

	lastChanged := currentState.LastChanged

	message, _ := json.Marshal(ApiStatus{Message: "Healthy!", LastChanged: lastChanged.String()})
	response.Write(message)
	return
}

func updateHandler(response http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	response.Header().Set("Content-Type", "application/json")

	bytes, err := ioutil.ReadAll(req.Body)
	if err != nil {
		message, _ := json.Marshal(ApiErrors{[]string{err.Error()}})
		response.Write(message)
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	state, err := catalog.Decode(bytes)
	if err != nil {
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	updateState(state)
}

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

func writeAndReload(state *catalog.ServicesState) {
	log.Info("Updating HAproxy")
	err := proxy.WriteAndReload(state)
	updateSuccess = (err == nil)
}

func updateState(state *catalog.ServicesState) {
	stateLock.Lock()
	defer stateLock.Unlock()
	currentState = state
	reloadChan <- time.Now().UTC()
}

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

	writeAndReload(state)

	return nil
}

func serveHttp(listenIp string, listenPort int) {
	listenStr := fmt.Sprintf("%s:%d", listenIp, listenPort)

	log.Infof("Starting up on %s", listenStr)
	router := mux.NewRouter()

	router.HandleFunc("/update", updateHandler).Methods("POST")
	router.HandleFunc("/health", healthHandler).Methods("GET")
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
