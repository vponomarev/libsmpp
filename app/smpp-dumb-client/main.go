package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"libsmpp"
	"net"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Remote string `yaml:"remote,omitempty"`
	Bind   struct {
		SystemID   string `yaml:"systemID,omitempty"`
		SystemType string `yaml:"systemType,omitempty"`
		Password   string `yaml:"password,omitempty"`
	}
	Message struct {
		From struct {
			TON  int    `yaml:"ton"`
			NPI  int    `yaml:"npi"`
			Addr string `yaml:"addr"`
		}
		To struct {
			TON  int    `yaml:"ton"`
			NPI  int    `yaml:"npi"`
			Addr string `yaml:"addr"`
		}
		RegisteredDelivery int    `yaml:"registeredDelivery"`
		DataCoding         int    `yaml:"dataCoding"`
		Body               string `yaml:"body"`
	}

	SendCount  int `yaml:"count"`
	SendRate   int `yaml:"rate"`
	SendWindow int `yaml:"window"`
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	log.WithFields(log.Fields{
		"type": "smpp-client",
	}).Info("Start")

	// Load configuration file
	config := Config{}

	configFileName := "config.yml"
	source, err := ioutil.ReadFile(configFileName)
	if err == nil {
		if err = yaml.Unmarshal(source, &config); err == nil {
			log.WithFields(log.Fields{
				"type": "smpp-client",
			}).Info("Loaded configuration file: ", configFileName)
		} else {
			fmt.Println("Error loading config file: ", err)
			return
		}
	}

	// Split REMOTE HOST:PORT
	remote := strings.Split(config.Remote, ":")
	if len(remote) != 2 {
		log.WithFields(log.Fields{"type": "smpp-client"}).Error("Cannot parse remote ip:port (", config.Remote, ")")
		return
	}

	// Check if Bind parameters are set (systemID at least)
	if len(config.Bind.SystemID) < 1 {
		log.WithFields(log.Fields{"type": "smpp-client"}).Error("bind/systemID is not specified")
		return
	}
	remoteIP := net.ParseIP(remote[0])
	if remoteIP == nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Error("Invalid destination IP:", remote[0])
	}

	remotePort, err := strconv.ParseUint(remote[1], 10, 16)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Error("Invalid destination Port:", remote[1])
	}

	// Init SMPP Session
	s := &libsmpp.SMPPSession{
		SessionID: 1,
	}
	s.Init()

	// Prepare SUBMIT_SM packet if specified
	// Encode packet
	rP, rErr := s.EncodeSubmitSm(libsmpp.SMPPSubmit{
		ServiceType: "",
		Source: libsmpp.SMPPAddress{
			TON:  uint8(config.Message.From.TON),
			NPI:  uint8(config.Message.From.NPI),
			Addr: config.Message.From.Addr,
		},
		Dest: libsmpp.SMPPAddress{
			TON:  uint8(config.Message.To.TON),
			NPI:  uint8(config.Message.To.NPI),
			Addr: config.Message.To.Addr,
		},
		ShortMessages:      config.Message.Body,
		RegisteredDelivery: uint8(config.Message.RegisteredDelivery),
	})
	if rErr != nil {
		fmt.Println("Error encoding packet body")
		return
	}

	dest := &net.TCPAddr{IP: remoteIP, Port: int(remotePort)}
	log.WithFields(log.Fields{"type": "smpp-client", "remoteIP": dest}).Info("Connecting to")
	conn, err := net.DialTCP("tcp", nil, dest)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-client", "service": "outConnect", "remoteIP": dest}).Error("Cannot connect to")
		return
	}

	go s.RunOutgoing(conn, libsmpp.SMPPBind{
		ConnMode:   libsmpp.CSMPPTRX,
		SystemID:   config.Bind.SystemID,
		Password:   config.Bind.Password,
		SystemType: config.Bind.SystemType,
		IVersion:   0x34,
	},
		1)

	for {
		select {
		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "StatusUpdate"}).Warning(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {

				// Send packet
				log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "SendPacket"}).Info("-")
				s.Outbox <- rP
			}

		case x := <-s.Inbox:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "Inbox"}).Info(x)

		case x := <-s.InboxR:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "InboxR"}).Info(x)

		case <-s.Closed:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "close"}).Warning("Connection is closed")
			return
		}
	}

}
