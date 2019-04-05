package main

import (
	"os"

	"github.com/BurntSushi/toml"
	"github.com/Nitro/sidecar/haproxy"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
)

type Config struct {
	HAproxyApi *ApiConfig       `toml:"haproxy_api" envconfig:"haproxy_api"`
	Sidecar    *SidecarConfig   `tom:"sidecar"`
	HAproxy    *haproxy.HAproxy `toml:"haproxy"`
}

type ApiConfig struct {
	BindIP       string `toml:"bind_ip" split_words:"true"`
	BindPort     int    `toml:"bind_port" split_words:"true"`
	LoggingLevel string `toml:"logging_level" split_words:"true"`
}

type SidecarConfig struct {
	StateUrl string `toml:"state_url" split_words:"true"`
}

func parseConfig(path string) *Config {
	var config Config
	_, err := toml.DecodeFile(path, &config)
	if err != nil {
		log.Errorf("Failed to parse config file: %s", err)
	}

	err = envconfig.Process("haproxy_api", &config)
	if err != nil {
		log.Errorf("Error processing envconfig: %s", err)
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
			proxy.PidFile + " ]] && echo \"-sf $(cat " + proxy.PidFile + ")\"`"
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
