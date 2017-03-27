package main

import (
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Nitro/sidecar/catalog"
	"github.com/Nitro/sidecar/haproxy"
	"github.com/Nitro/sidecar/service"
	log "github.com/Sirupsen/logrus"
	"github.com/mitchellh/go-ps"
	"github.com/relistan/go-director"
	"github.com/relistan/rubberneck"
	"gopkg.in/alecthomas/kingpin.v2"
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
)

type CliOpts struct {
	ConfigFile *string
	Follow     *string
}

func parseCommandLine() *CliOpts {
	var opts CliOpts

	app := kingpin.New("haproxy-api", "").DefaultEnvars()
	opts.ConfigFile = app.Flag("config-file", "The config file to use").
		Short('f').Default("haproxy-api.toml").String()
	opts.Follow = app.Flag("follow", "Actively follow this Sidecar's /watch endpoint (format ip:port)").
		Short('F').String()

	app.Parse(os.Args[1:])

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

// Check all the state transitions and only update HAproxy when a change
// will affect service availability.
func maybeNotify(oldStatus int, newStatus int) {
	updated := false

	log.Debugf("Checking event. OldStatus: %s NewStatus: %s",
		service.StatusString(oldStatus), service.StatusString(newStatus),
	)

	// Compare old and new states to find significant changes only
	switch newStatus {
	case service.ALIVE:
		updated = true
		enqueueUpdate()
	case service.TOMBSTONE:
		updated = true
		enqueueUpdate()
	case service.UNKNOWN:
		if oldStatus == service.ALIVE {
			updated = true
			enqueueUpdate()
		}
	case service.UNHEALTHY:
		if oldStatus == service.ALIVE {
			updated = true
			enqueueUpdate()
		}
	default:
		log.Errorf("Got unknown service change status: %d", newStatus)
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
func fetchState(url string) (*catalog.ServicesState, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	state, err := catalog.Decode(bytes)
	if err != nil {
		return nil, err
	}

	return state, nil
}

// Loops in the background, waiting to be notified that something
// has changed. When a change is received, we fetch the new state
// (what we got wasn't in a useful format) and notify HAproxy.
func processFollower(url string, looper director.Looper, notifyChan chan struct{}) {
	looper.Loop(func() error {
		<-notifyChan
		state, err := fetchState(url)
		if err != nil {
			log.Errorf("Unable to fetch Sidecar state: %s", err.Error())
			return nil
		}

		// Replace the current state with the new one
		stateLock.Lock()
		currentState = state
		stateLock.Unlock()

		enqueueUpdate()
		return nil
	})
}

// When we're in follow mode, do the business
func handleFollowing(stateUrl string, watchUrl string, watchLooper director.Looper, processLooper director.Looper) {
	notifyChan := make(chan struct{})

	go processFollower(stateUrl, processLooper, notifyChan)

	watcher := NewSidecarWatcher(watchUrl, watchLooper, notifyChan)
	watcher.Follow()
}

// Construct the watch and state URLs from the opts and config provided
func generateUrls(opts *CliOpts, config *Config) (watchUrl string, stateUrl string) {
	if *opts.Follow == "" {
		stateUrl = config.Sidecar.StateUrl
	} else {
		stateTmp, err := url.Parse("http://" + *opts.Follow)
		if err != nil {
			log.Fatalf("Unable to follow %s: %s", *opts.Follow, err)
		}
		stateTmp.Path = "/state.json"
		stateUrl = stateTmp.String()

		watchTmp, err := url.Parse("http://" + *opts.Follow)
		if err != nil {
			log.Fatalf("Unable to follow %s: %s", *opts.Follow, err)
		}
		watchTmp.Path = "/watch"
		watchUrl = watchTmp.String()
	}

	return watchUrl, stateUrl
}

// On startup in standard mode, we need to bootstrap some state
func fetchInitialState(stateUrl string) {
	log.Info("Fetching initial state on startup...")
	state, err := fetchState(stateUrl)
	if err != nil {
		log.Errorf("Failed to fetch state from '%s'... continuing in hopes someone will post it", stateUrl)
	} else {
		log.Info("Successfully retrieved state")
		currentState = state
		writeAndReload(state)
	}
}

func printConfig(opts *CliOpts, config *Config) {
	printer := rubberneck.NewPrinter(log.Infof, rubberneck.NoAddLineFeed)
	printer.PrintWithLabel("HAproxy-API starting", opts, config)
}

// See if the pid file and a running process match. Otherwise
// this makes things unhappy when we try to manage HAproxy
func checkHAproxyPidFile(config *Config) {
	// We don't care about this if the command doesn't refer to a pid
	if !strings.Contains(config.HAproxy.ReloadCmd, ".pid") {
		return
	}

	var foundProc ps.Process
	procs, err := ps.Processes()
	if err != nil {
		log.Fatalf("Unable to read process table! %s", err)
	}

	foundCount := 0
	for _, process := range procs {
		if strings.Contains(process.Executable(), "haproxy") {
			foundProc = process
			foundCount += 1
		}
	}

	if foundCount > 2 {
		log.Fatalf("There already appears to be %d HAproxies running. Please clean up.", foundCount-1)
	}

	if foundProc != nil {
		storedPid, err := ioutil.ReadFile(config.HAproxy.PidFile)
		if err != nil || (strconv.Itoa(foundProc.Pid()) != string(storedPid)) {
			log.Warnf("pid file appears bogus, writing pid file with pid %v", foundProc.Pid())
			err = ioutil.WriteFile(config.HAproxy.PidFile, []byte(strconv.Itoa(foundProc.Pid())), 0640)
			if err != nil {
				log.Fatalf("Unable to write new pid file. Please clean up by hand! %s", err)
			}
		}

		return
	}

	// If the pid file also doesn't exist, we can move on
	if _, err := os.Stat(config.HAproxy.PidFile); os.IsNotExist(err) {
		return
	}

	// Nothing found, let's make sure the PidFile is gone
	log.Warn("Removing stale pid file")
	err = os.Remove(config.HAproxy.PidFile)
	if err != nil {
		log.Fatalf("HAproxy not running, pid file exists, but can't remove it! %s", err)
	}
}

func main() {
	opts := parseCommandLine()
	config := parseConfig(*opts.ConfigFile)

	printConfig(opts, config)

	proxy = config.HAproxy

	reloadChan = make(chan time.Time, RELOAD_BUFFER)
	watchUrl, stateUrl := generateUrls(opts, config)

	// If we're in follow mode, do that
	if *opts.Follow != "" {
		log.Info("Running in follower mode")
		checkHAproxyPidFile(config)
		watchLooper := director.NewFreeLooper(director.FOREVER, make(chan error))
		processLooper := director.NewFreeLooper(director.FOREVER, make(chan error))
		go handleFollowing(stateUrl, watchUrl, watchLooper, processLooper)
	} else {
		fetchInitialState(stateUrl)
	}

	// Watch for updates and handle reloading HAproxy
	go processUpdates()

	// Run the web API and block until it completes
	serveHttp(config.HAproxyApi.BindIP, config.HAproxyApi.BindPort)

	close(reloadChan)
}
