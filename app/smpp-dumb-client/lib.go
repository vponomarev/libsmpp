package main

import (
	"fmt"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net"
	"strconv"
	"strings"
)

func loadConfig(configFileName string) (config Config, params Params, err error) {
	config = Config{}
	params = Params{}

	source, err := ioutil.ReadFile(configFileName)
	if err != nil {
		err = fmt.Errorf("cannot read config file [%s]", configFileName)
		return
	}

	if err = yaml.Unmarshal(source, &config); err != nil {
		err = fmt.Errorf("error parsing config file [%s]: %v", configFileName, err)
		return
	}
	log.WithFields(log.Fields{"type": "smpp-client"}).Info("Loaded configuration file: ", configFileName)

	// Load ENV configuration
	if err = envconfig.Process("", &config); err != nil {
		err = fmt.Errorf("error parsing ENVIRONMENT configuration: %v", err)
		return
	}

	// Load LogLevel from config if present
	if len(config.Log.Level) > 0 {
		if l, err := log.ParseLevel(config.Log.Level); err == nil {
			log.SetLevel(l)
			log.WithFields(log.Fields{"type": "smpp-client"}).Warning("Switch LogLevel to: ", l.String())
		}
	}

	// Split REMOTE HOST:PORT
	remote := strings.Split(config.SMPP.Remote, ":")
	if len(remote) != 2 {
		err = fmt.Errorf("cannot parse remote ip:port (%s)", config.SMPP.Remote)
		return
	}

	params.remoteIP = net.ParseIP(remote[0])
	if params.remoteIP == nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Invalid destination IP:", remote[0])
		return
	}

	remotePort, err := strconv.ParseUint(remote[1], 10, 16)
	if err != nil {
		err = fmt.Errorf("invalid destination Port: %s", remote[1])
		return
	}
	params.remotePort = int(remotePort)

	// Check if Bind parameters are set (systemID at least)
	if len(config.SMPP.Bind.SystemID) < 1 {
		err = fmt.Errorf("bind/systemID is not specified")
		return
	}

	// Check bind mode (TRX/TX/RX)
	switch config.SMPP.Bind.Mode {
	case "TX":
		params.bindMode = libsmpp.CSMPPTX
	case "RX":
		params.bindMode = libsmpp.CSMPPRX
	case "TRX":
		params.bindMode = libsmpp.CSMPPTRX
	default:
		err = fmt.Errorf("invalid connection mode: %s (supported only: TX, RX, TRX)", config.SMPP.Bind.Mode)
		return
	}

	// Prepare SUBMIT_SM packet if specified
	if params.submit, err = prepareSubmit(config); err != nil {
		err = fmt.Errorf("error loading submit parameters: %v", err)
		return
	}

	// Try to encode packet with specified parameters
	s := &libsmpp.SMPPSession{}
	if _, err = s.EncodeSubmitSm(params.submit); err != nil {
		err = fmt.Errorf("error encoding packet body: %v", err)
		return
	}

	return
}
