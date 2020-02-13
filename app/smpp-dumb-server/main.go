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
	Port     int    `yaml:"port,omitempty"`
	LogLevel string `yaml:"logLevel,omitempty"`
	Logging  struct {
		Server struct {
			Rate bool
		}
	}
}

type Params struct {
	LogLevel log.Level
	Flags    struct {
		LogLevel bool
	}
}

func ProcessCMDLine() (p Params) {
	// Set default
	p.LogLevel = log.InfoLevel

	var pv bool
	var pvn string
	for _, param := range os.Args[1:] {
		if pv {
			switch pvn {
			// LogLevel
			case "-log":
				l, err := log.ParseLevel(param)
				if err != nil {
					l = log.InfoLevel
					fmt.Print("Incorrect LogLevel [", param, "], set LogLevel to: ", l.String())
				} else {
					p.LogLevel = l
					p.Flags.LogLevel = true
				}
			}
		} else {
			switch param {
			case "-log":
				pvn = param
				pv = true
			}
		}
	}
	return p
}

func main() {
	pParam := ProcessCMDLine()

	fmt.Println("LogLevel:", pParam.LogLevel.String())
	log.SetOutput(os.Stdout)
	log.SetLevel(pParam.LogLevel)

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

	// Load LogLevel from config if present
	if (len(config.LogLevel) > 0) && (!pParam.Flags.LogLevel) {
		if l, err := log.ParseLevel(config.LogLevel); err == nil {
			pParam.LogLevel = l

			log.SetLevel(pParam.LogLevel)
			log.WithFields(log.Fields{
				"type": "smpp-server",
			}).Warning("Override LogLevel to: ", pParam.LogLevel.String())
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
			log.WithFields(log.Fields{"type": "smpp-server"}).Error("Error accepting socket connection: ", err)
			return
		}
		log.WithFields(log.Fields{"type": "smpp-server", "remoteIP": conn.RemoteAddr().String()}).Warning("Received incoming connectiton")
		go hConn(id, conn, config)
	}
}

func hConn(id uint32, conn *net.TCPConn, config Config) {
	// Allocate new SMPP Session structure
	s := &libsmpp.SMPPSession{
		ManualBindValidate: true,
		DebugLevel:         1,
		SessionID:          id,
	}
	s.Init()

	go s.RunIncoming(conn, id)

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
			log.WithFields(log.Fields{"type": "smpp-server", "SID": s.SessionID, "service": "inConnect", "action": "StatusUpdate"}).Info(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {
				// Pass session to SessionPool
				sessionProcessor(s, config)
				return
			}

		case <-s.Closed:
			fmt.Println("[", id, "] Connection is closed!")
			return
		}
	}
}

func sessionProcessor(s *libsmpp.SMPPSession, config Config) {
	var msgID uint32
	msgID = 1

	if config.Logging.Server.Rate {
		go func(msgID *uint32, s *libsmpp.SMPPSession) {
			var sv uint32
			sv = 0
			c := time.Tick(1000 * time.Millisecond)
			for {

				select {
				case <-c:
					sn := *msgID
					if sn > sv {
						fmt.Println("[", s.SessionID, "] During last 1s: ", (sn - sv), " [MAX:", sn, "]")
						sv = sn
					} else {
						fmt.Println("[", s.SessionID, "] During last 1s: -")
					}
				case <-s.Closed:
					return
				}
			}
		}(&msgID, s)
	}

	for {
		select {
		case p := <-s.Inbox:
			log.WithFields(log.Fields{"type": "smpp-server", "service": "PacketLoop", "SID": s.SessionID, "action": fmt.Sprintf("%x (%s)", p.Hdr.ID, libsmpp.CmdName(p.Hdr.ID)), "Seq": p.Hdr.Seq, "Len": p.Hdr.Len}).Trace(fmt.Sprintf("%x", p.Body))

			// Confirm packet
			pR := s.EncodeSubmitSmResp(p, 0, fmt.Sprintf("%06x", msgID))
			s.Outbox <- pR
			msgID++

			// Generate DELIVERY REPORT in a separate go thread
			go func(p libsmpp.SMPPPacket) {
				pD, perr := s.DecodeSubmitDeliverSm(&p)
				if perr != nil {
					return
				}
				// SWAP Source <=> Dest
				ax := pD.Source
				pD.Source = pD.Dest
				pD.Dest = ax

				// Fill Message Text
				pD.ShortMessages = "id:1234567890 sub:001 dlvrd:000 submit date:0000000000 done date:0000000000 stat:REJECTD err:000 text:"
				pE, perr := s.EncodeDeliverSm(pD)
				if perr != nil {
					return
				}

				s.Outbox <- pE
			}(p)

		case p := <-s.InboxR:
			log.WithFields(log.Fields{"type": "smpp-server", "service": "PacketLoopR", "SID": s.SessionID, "action": fmt.Sprintf("%x (%s)", p.Hdr.ID, libsmpp.CmdName(p.Hdr.ID)), "Seq": p.Hdr.Seq, "Len": p.Hdr.Len}).Trace(fmt.Sprintf("%x", p.Body))

		case <-s.Closed:
			return
		}
	}
}
