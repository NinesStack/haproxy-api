package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"

	"github.com/BurntSushi/toml"
	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/newrelic/sidecar/catalog"
	"github.com/newrelic/sidecar/haproxy"
	"gopkg.in/alecthomas/kingpin.v1"
)

const (
	listen_port = 7778
)

var proxy *haproxy.HAproxy

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

func parseConfig(path string) *haproxy.HAproxy{
	var config haproxy.HAproxy
	_, err := toml.DecodeFile(path, &config)
	if err != nil {
		exitWithError(err, "Failed to parse config file")
	}

	return &config
}

func parseCommandLine() *CliOpts {
	var opts CliOpts
	opts.ConfigFile = kingpin.Flag("config-file", "The config file to use").Short('f').Default("haproxy.toml").String()
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

	err := run("ps auxww | grep -v haproxy-api | grep [h]aproxy")
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

	log.Info("Updating state")
	proxy.WriteAndReload(state)
}

func fetchState(url string) error {
	resp, err := http.Get(url)
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
	proxy.WriteAndReload(state)

	return nil
}

func serveHttp() {
	log.Infof("Starting up on 0.0.0.0:%d", listen_port)
	router := mux.NewRouter()

	router.HandleFunc("/update", updateHandler).Methods("POST")
	router.HandleFunc("/health", healthHandler).Methods("GET")
	http.Handle("/", handlers.LoggingHandler(os.Stdout, router))

	err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", listen_port), nil)
	if err != nil {
		log.Fatalf("Can't start http server: %s", err.Error())
	}
}

func main() {
	opts := parseCommandLine()
	proxy = parseConfig(*opts.ConfigFile) // Cheat and just parse the config right into the struct
	proxy.ReloadCmd = "haproxy -f " + proxy.ConfigFile + " -p " + proxy.PidFile + " `[[ -f " + proxy.PidFile + " ]] && echo \"-sf $(cat " + proxy.PidFile + ")\"]]`"
	proxy.VerifyCmd = "haproxy -c -f " + proxy.ConfigFile

	log.Info("Fetching initial state on startup...")
	err := fetchState(fmt.Sprintf("http://%s:7777/state", proxy.BindIP))
	if err != nil {
		log.Error("Failed to fetch state... continuing in hopes someone will post it")
	}

	serveHttp()
}
