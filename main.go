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

var (
	proxy     *haproxy.HAproxy
	proxyLock *sync.Mutex
)

type CliOpts struct {
	ConfigFile *string
}

type ApiError struct {
	Error string `json:"error"`
}

type ApiMessage struct {
	Message string `json:"message"`
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

	err := run("test -f " + proxy.PidFile + " && ps aux `cat " + proxy.PidFile + "`")
	if err != nil {
		message, _ := json.Marshal(ApiError{"No HAproxy running!"})
		response.Write(message)
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	message, _ := json.Marshal(ApiMessage{"Healthy!"})
	response.Write(message)
	return
}

func updateHandler(response http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	response.Header().Set("Content-Type", "application/json")

	bytes, err := ioutil.ReadAll(req.Body)
	if err != nil {
		message, _ := json.Marshal(ApiError{err.Error()})
		response.Write(message)
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	state, err := catalog.Decode(bytes)
	if err != nil {
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	writeAndReload(state)
}

func writeAndReload(state *catalog.ServicesState) {
	// We really, really don't want to be doing this more
	// than once at a time. Since each request is already on its
	// own goroutine, let's just use that and synchronize.
	proxyLock.Lock()
	defer proxyLock.Unlock()

	log.Info("Updating state")
	proxy.WriteAndReload(state)
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

	log.Info("Updating state")
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

	log.Info("Fetching initial state on startup...")
	err := fetchState(config.Sidecar.StateUrl)
	if err != nil {
		log.Errorf("Failed to fetch state from '%s'... continuing in hopes someone will post it", config.Sidecar.StateUrl)
	} else {
		log.Info("Successfully retrieved state")
	}

	serveHttp(config.HAproxyApi.BindIP, config.HAproxyApi.BindPort)
}
