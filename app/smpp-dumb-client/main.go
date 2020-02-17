package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"libsmpp"
	libsmppConst "libsmpp/const"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Remote   string `yaml:"remote,omitempty"`
	LogLevel string `yaml:"logLevel,omitempty"`
	Bind     struct {
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

type Params struct {
	LogLevel log.Level
	Flags    struct {
		LogLevel bool
	}
}

type TrackProcessingTime struct {
	Count      uint
	DelayMin   time.Duration
	DelayMax   time.Duration
	DelayTotal time.Duration
	sync.RWMutex
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

	// Load LogLevel from config if present
	if (len(config.LogLevel) > 0) && (!pParam.Flags.LogLevel) {
		if l, err := log.ParseLevel(config.LogLevel); err == nil {
			pParam.LogLevel = l

			log.SetLevel(pParam.LogLevel)
			log.WithFields(log.Fields{
				"type": "smpp-client",
			}).Warning("Override LogLevel to: ", pParam.LogLevel.String())
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

	// Track message processing time
	TimeTracker := TrackProcessingTime{}

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

	// Handle `SEND COMPLETE` event
	SendCompleteCH := make(chan interface{})

	for {
		select {
		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "StatusUpdate"}).Warning(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {

				// Start packet submission
				log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "SendPacket", "count": config.SendCount, "rate": config.SendRate}).Info("Start message bulk message submission")
				go PacketSender(s, rP, uint(config.SendRate), uint(config.SendCount), &TimeTracker, SendCompleteCH)
			}

		case x := <-s.Inbox:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "Inbox"}).Debug(x)

			// Generate confirmation for DeliverSM
			if x.Hdr.ID == libsmppConst.CMD_DELIVER_SM {
				s.Outbox <- s.EncodeDeliverSmResp(x, libsmppConst.ESME_ROK)
			}

		case x := <-s.InboxR:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "InboxR"}).Debug(x)

			TimeTracker.Lock()
			TimeTracker.Count++
			rtd := x.CreateTime.Sub(x.OrigTime)
			TimeTracker.DelayTotal += rtd
			TimeTracker.Unlock()

			/*
				fmt.Println("Packet:")
				fmt.Println("CreateTime:", x.CreateTime)
				fmt.Println("NetSendTime:", x.NetSentTime)
				fmt.Println("OrigTime:", x.OrigTime)
				fmt.Println("NetOrigTime:", x.NetOrigTime)
			*/
			//			fmt.Println("Round Trip Delay: ", rtd)
		case <-s.Closed:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "close"}).Warning("Connection is closed")
			return
		case <-SendCompleteCH:
			s.Close("Send complete")
		}
	}
}

// Send messages
func PacketSender(s *libsmpp.SMPPSession, p libsmpp.SMPPPacket, rate uint, cnt uint, TimeTracker *TrackProcessingTime, SendCompleteCH chan interface{}) {
	// Sleep for 3s after finishing sending and close trigger channel
	defer func() {
		time.Sleep(3 * time.Second)
		close(SendCompleteCH)
	}()

	var tick time.Duration
	var tickCouter uint
	var tickInfoModule uint
	var blockSize uint
	var msgLastSec uint
	// For rate > 100 SMS/sec send send messages each 10 ms
	if rate > 100 {
		tick = 10 * time.Millisecond
		blockSize = rate / 100
		tickInfoModule = 100
	} else if rate > 10 {
		// For rate > 10 SMS/sec AND <= 100 SMS/sec send messages each 100 ms
		tick = 100 * time.Millisecond
		blockSize = rate / 10
		tickInfoModule = 10
	} else if rate > 1 {
		// For rate > 1 SMS/sec AND <= 10 SMS/sec send message each 500 ms
		tick = 500 * time.Millisecond
		blockSize = rate / 2
		tickInfoModule = 2
	} else {
		// Rate is 1 SMS/sec
		tick = 1 * time.Second
		blockSize = rate
		tickInfoModule = 1
	}

	var done uint
	c := time.Tick(tick)
	for {
		select {
		case <-c:
			tickCouter++
			var i uint
			for ; (i < blockSize) && (done < cnt); i++ {
				p.CreateTime = time.Now()
				s.Outbox <- p
				msgLastSec++
				done++
			}
			if done >= cnt {
				fmt.Println("#Finished sending", cnt, "messages with rate", rate)
				return
			}

			if tickCouter%tickInfoModule == 0 {
				tx, rx := s.GetTrackQueueSize()
				TimeTracker.Lock()
				tCnt := TimeTracker.Count
				tDur := TimeTracker.DelayTotal
				TimeTracker.Count = 0
				TimeTracker.DelayTotal = 0
				TimeTracker.Unlock()

				var tAvg int64
				if tCnt > 0 {
					tAvg = tDur.Microseconds() / int64(tCnt)
				}

				fmt.Println("[", s.SessionID, "] During last 1s: ", msgLastSec, " [MAX:", done, "][TX:", tx, "][RX:", rx, "][RTDavg micros: ", tAvg, ",", tCnt, "]")
				msgLastSec = 0
			}

		case <-s.Closed:
			fmt.Println("#ClosedCH")
			return
		}
	}

}
