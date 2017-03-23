package main

import (
	"os"

	"github.com/BurntSushi/toml"
	"github.com/Nitro/sidecar/haproxy"
	log "github.com/Sirupsen/logrus"
)

type Config struct {
	HAproxy    *haproxy.HAproxy `toml:"haproxy"`
	HAproxyApi *ApiConfig       `toml:"haproxy_api"`
	Sidecar    *SidecarConfig   `tom:"sidecar"`
}

type ApiConfig struct {
	BindIP       string `toml:"bind_ip"`
	BindPort     int    `toml:"bind_port"`
	LoggingLevel string `toml:"logging_level"`
}

type SidecarConfig struct {
	StateUrl string `toml:"state_url"`
}

func parseConfig(path string) *Config {
	var config Config
	_, err := toml.DecodeFile(path, &config)
	if err != nil {
		exitWithError(err, "Failed to parse config file")
	}

	proxy := config.HAproxy
	if proxy == nil {
		log.Error("Missing 'haproxy' section of config file")
		os.Exit(1)
	}

	// Set some defaults if not provided. These should mostly
	// do the right thing unless this is not running in the
	// standard Docker container.
	if proxy.ReloadCmd == "" {
		proxy.ReloadCmd = "haproxy -f " + proxy.ConfigFile + " -p " +
			proxy.PidFile + " `[[ -f " +
			proxy.PidFile + " ]] && echo \"-sf $(cat " + proxy.PidFile + ")\"]]`"
	}

	if proxy.VerifyCmd == "" {
		proxy.VerifyCmd = "haproxy -c -f " + proxy.ConfigFile
	}

	if config.HAproxyApi.BindIP == "" {
		config.HAproxyApi.BindIP = "0.0.0.0"
	}

	if config.HAproxyApi.BindPort == 0 {
		config.HAproxyApi.BindPort = 7778
	}

	configureLoggingLevel(config.HAproxyApi.LoggingLevel)

	return &config
}

func configureLoggingLevel(level string) {
	switch {
	case len(level) == 0:
		log.SetLevel(log.InfoLevel)
	case level == "info":
		log.SetLevel(log.InfoLevel)
	case level == "warn":
		log.SetLevel(log.WarnLevel)
	case level == "error":
		log.SetLevel(log.ErrorLevel)
	case level == "debug":
		log.SetLevel(log.DebugLevel)
	}
}
