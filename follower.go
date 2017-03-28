package main

import (
	"io/ioutil"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Nitro/sidecar/receiver"
	"github.com/mitchellh/go-ps"
	log "github.com/Sirupsen/logrus"
	"github.com/relistan/go-director"
)

// Loops in the background, waiting to be notified that something
// has changed. When a change is received, we fetch the new state
// (what we got wasn't in a useful format) and notify HAproxy.
func processFollower(url string, looper director.Looper, notifyChan chan struct{}, rcvr *receiver.Receiver) {
	looper.Loop(func() error {
		<-notifyChan
		state, err := receiver.FetchState(url)
		if err != nil {
			log.Errorf("Unable to fetch Sidecar state: %s", err.Error())
			return nil
		}

		// Replace the current state with the new one
		rcvr.StateLock.Lock()
		rcvr.CurrentState = state
		rcvr.StateLock.Unlock()

		rcvr.EnqueueUpdate()
		return nil
	})
}

// When we're in follow mode, do the business
func handleFollowing(stateUrl string, watchUrl string, watchLooper director.Looper, processLooper director.Looper, rcvr *receiver.Receiver) {
	notifyChan := make(chan struct{})

	go processFollower(stateUrl, processLooper, notifyChan, rcvr)

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
