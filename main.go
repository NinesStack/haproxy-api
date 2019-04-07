package main

import (
	"os"
	"os/exec"

	"github.com/Nitro/sidecar/catalog"
	"github.com/Nitro/sidecar/haproxy"
	"github.com/Nitro/sidecar/receiver"
	"github.com/relistan/go-director"
	"github.com/relistan/rubberneck"
	log "github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	ReloadBufferSize = 256
)

var (
	proxy         *haproxy.HAproxy
	updateSuccess bool
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

// Write out the HAproxy config and reload the instance
func writeAndReload(state *catalog.ServicesState) {
	log.Info("Updating HAproxy")
	err := proxy.WriteAndReload(state)
	if err != nil {
		log.Errorf("Failed updating HAproxy: %s", err)
	} else {
		log.Info("Success updating HAproxy")
	}
	updateSuccess = (err == nil)
}

func printConfig(opts *CliOpts, config *Config) {
	printer := rubberneck.NewPrinter(log.Infof, rubberneck.NoAddLineFeed)
	printer.PrintWithLabel("HAproxy-API starting", opts, config)
}

func main() {
	opts := parseCommandLine()
	config := parseConfig(*opts.ConfigFile)

	printConfig(opts, config)

	proxy = config.HAproxy

	rcvr := receiver.NewReceiver(ReloadBufferSize, writeAndReload)
	watchUrl, stateUrl := generateUrls(opts, config)

	// If we're in follow mode, do that
	if *opts.Follow != "" {
		log.Info("Running in follower mode")
		checkHAproxyPidFile(config)
		watchLooper := director.NewFreeLooper(director.FOREVER, make(chan error))
		processLooper := director.NewFreeLooper(director.FOREVER, make(chan error))
		go handleFollowing(stateUrl, watchUrl, watchLooper, processLooper, rcvr)
	} else {
		err := rcvr.FetchInitialState(stateUrl)
		if err == nil {
			writeAndReload(rcvr.CurrentState)
		} else {
			log.Errorf("Failed to fetch state from '%s'... continuing in hopes someone will post it", stateUrl)
		}
	}

	// Watch for updates and handle reloading HAproxy
	go rcvr.ProcessUpdates()

	// Run the web API and block until it completes
	serveHttp(config.HAproxyApi.BindIP, config.HAproxyApi.BindPort, rcvr)
}
