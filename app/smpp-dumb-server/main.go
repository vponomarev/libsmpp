package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"libsmpp"
	"net"
	"os"
	"time"
)

type Config struct {
	Port int `yaml:"port,omitempty"`
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	log.WithFields(log.Fields{
		"type": "smpp-server",
	}).Info("Start")

	// Load configuration file
	var config Config

	configFileName := "config.yml"
	source, err := ioutil.ReadFile(configFileName)
	if err == nil {
		if err = yaml.Unmarshal(source, &config); err == nil {
			log.WithFields(log.Fields{
				"type": "smpp-server",
			}).Info("Loaded configuration file: ", configFileName)
		}
	}

	// Fill default values for config
	if config.Port < 1 {
		config.Port = 2775
	}
	// Listen socket for new connections
	lAddr := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: config.Port}

	socket, err := net.ListenTCP("tcp4", lAddr)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-server"}).Error("Error listening TCP socket: ", err)
		return
	}
	log.WithFields(log.Fields{"type": "smpp-server", "service": "ListenTCP"}).Warning("Starting listening on: ", socket.Addr().String())

	var id uint32
	for {
		conn, err := socket.AcceptTCP()
		id++
		if err != nil {
			log.WithFields(log.Fields{"type": "smpp-lb"}).Error("Error accepting socket connection: ", err)
			return
		}
		log.WithFields(log.Fields{"type": "smpp-lb", "remoteIP": conn.RemoteAddr().String()}).Warning("Received incoming conneciton")
		go hConn(id, conn)
	}
}

func hConn(id uint32, conn *net.TCPConn) {
	// Allocate new SMPP Session structure
	s := &libsmpp.SMPPSession{
		ManualBindValidate: true,
		DebugLevel:         1,
		SessionID:          id,
	}
	s.Init()

	go s.RunIncoming(conn, id)

	var msgID uint
	msgID = 1

	go func(msgID *uint, s *libsmpp.SMPPSession) {
		var sv uint
		sv = 0
		c := time.Tick(1000 * time.Millisecond)
		for {

			select {
			case <-c:
				sn := *msgID
				if sn > sv {
					fmt.Println("[", s.SessionID, "] During last 1s: ", (sn - sv))
					sv = sn
				} else {
					fmt.Println("[", s.SessionID, "] During last 1s: -")
				}
			case <-s.Closed:
				return
			}
		}
	}(&msgID, s)

	for {
		select {
		// Request for BIND validation
		case x := <-s.BindValidator:
			r := libsmpp.BindValidatorResponce{
				ID:     x.ID,
				SMSCID: "GoLib32",
				Status: 0,
			}
			s.BindValidatorR <- r

		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "smpp-lb", "SID": s.SessionID, "service": "inConnect", "action": "StatusUpdate"}).Warning(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {
				// Pass session to SessionPool
				sessionProcessor(s)
				return
			}

		case <-s.Closed:
			fmt.Println("[", id, "] Connection is closed!")
			return
		}
	}
}

func sessionProcessor(s *libsmpp.SMPPSession) {

	var msgID uint32
	msgID = 1

	for {
		select {
		case p := <-s.Inbox:
			fmt.Println("[", s.SessionID, "] Incoming packet: ", p)

			// Confirm packet
			pR := s.EncodeSubmitSmResp(p, 0, fmt.Sprintf("%06x", msgID))
			msgID++

			s.Outbox <- pR

		case <-s.Closed:
			return
		}
	}
}
