package main

import (
	"encoding/hex"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	libsmppConst "github.com/vponomarev/libsmpp/const"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Log struct {
		Level  string `yaml:"level,omitempty"`
		Rate   bool
		Netbuf bool `yaml:"netbuf"`
	}

	Remote         string `yaml:"remote,omitempty"`
	Profiler       bool   `yaml:"profiler,omitempty"`
	ProfilerListen string `yaml:"profilerListen,omitempty"`
	Bind           struct {
		SystemID   string `yaml:"systemID,omitempty"`
		SystemType string `yaml:"systemType,omitempty"`
		Password   string `yaml:"password,omitempty"`
		Mode       string `yaml:"mode",omitempty`
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
		RegisteredDelivery int      `yaml:"registeredDelivery"`
		DataCoding         int      `yaml:"dataCoding"`
		Body               string   `yaml:"body"`
		TLV                []string `yaml:"tlv"`
	}

	SendCount  uint `yaml:"count"`
	SendRate   uint `yaml:"rate"`
	SendWindow uint `yaml:"window"`

	StayConnected bool `yaml:"stayConnected,omitempty"`
}

type Params struct {
	LogLevel       log.Level
	ConfigFileName string
	Flags          struct {
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
	p.ConfigFileName = "config.yml"

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
			case "-config":
				p.ConfigFileName = param
			}
		} else {
			switch param {
			case "-log":
				pvn = param
				pv = true
			case "-config":
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
	fmt.Println("Config file:", pParam.ConfigFileName)

	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	log.WithFields(log.Fields{
		"type": "smpp-client",
	}).Info("Start")

	// Load configuration file
	config := Config{}

	source, err := ioutil.ReadFile(pParam.ConfigFileName)
	if err == nil {
		if err = yaml.Unmarshal(source, &config); err == nil {
			log.WithFields(log.Fields{
				"type": "smpp-client",
			}).Info("Loaded configuration file: ", pParam.ConfigFileName)
		} else {
			fmt.Println("Error loading config file: ", err)
			return
		}
	}

	// Load LogLevel from config if present
	if (len(config.Log.Level) > 0) && (!pParam.Flags.LogLevel) {
		if l, err := log.ParseLevel(config.Log.Level); err == nil {
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
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Cannot parse remote ip:port (", config.Remote, ")")
		return
	}

	// Check if Bind parameters are set (systemID at least)
	if len(config.Bind.SystemID) < 1 {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("bind/systemID is not specified")
		return
	}
	remoteIP := net.ParseIP(remote[0])
	if remoteIP == nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Invalid destination IP:", remote[0])
		return
	}

	remotePort, err := strconv.ParseUint(remote[1], 10, 16)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Invalid destination Port:", remote[1])
		return
	}

	// Check bind mode (TRX/TX/RX)
	var cm libsmpp.ConnSMPPMode
	switch config.Bind.Mode {
	case "TX":
		cm = libsmpp.CSMPPTX
	case "RX":
		cm = libsmpp.CSMPPRX
	case "TRX":
		cm = libsmpp.CSMPPTRX
	default:
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Invalid connection mode:", config.Bind.Mode, " (supported only: TX, RX, TRX)")
		return
	}

	// Init profiler if enabled
	if config.Profiler {
		if len(config.ProfilerListen) == 0 {
			config.ProfilerListen = "127.0.0.1:5800"
		}
		log.WithFields(log.Fields{"type": "smpp-client", "action": "profiler"}).Info("Starting profiler at: ", config.ProfilerListen)

		go func(addr string) {
			err := http.ListenAndServe(addr, nil)
			if err != nil {
				log.WithFields(log.Fields{"type": "smpp-client", "action": "profiler"}).Fatal("ListenAndServe returned an error: ", err)
				return
			}
		}(config.ProfilerListen)
	}

	// Init SMPP Session
	s := &libsmpp.SMPPSession{
		SessionID:   1,
		DebugNetBuf: config.Log.Netbuf,
	}
	s.Init()

	// Prepare SUBMIT_SM packet if specified
	oP := libsmpp.SMPPSubmit{
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
		TLV:                map[libsmpp.TLVCode]libsmpp.TLVStruct{},
	}
	for _, tlv := range config.Message.TLV {
		tEntity := strings.Split(tlv, ";")
		if len(tEntity) != 3 {
			log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error parsing TLV [", tlv, "] - should be 3 params")
			return
		}

		tVal := strings.Trim(tEntity[2], " ")
		if tVal[0] != '"' || tVal[len(tVal)-1] != '"' {
			log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error parsing TLV [", tlv, "] - take value into quotes")
			return
		}
		tVal = strings.Trim(tVal, "\"")

		var tK int64
		var tV []byte
		var err error
		if (len(tEntity[0]) > 2) && (tEntity[0][0:2] == "0x") {
			if tK, err = strconv.ParseInt(tEntity[0][2:], 16, 16); err != nil {
				log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error parsing TLV [", tlv, "] - HEX key [", tEntity[0], "]: ", err)
				return
			}
		} else {
			if tK, err = strconv.ParseInt(tEntity[0], 10, 16); err != nil {
				log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error parsing TLV [", tlv, "] - HEX key [", tEntity[0], "]: ", err)
				return
			}
		}
		switch strings.Trim(tEntity[1], " ") {
		case "string":
			tV = []byte(tVal)
		case "hex":
			if tV, err = hex.DecodeString(tVal); err != nil {
				log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error parsing TLV [", tlv, "] - HEX value [", tEntity[2], "]: ", err)
				return
			}
		default:
			log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error parsing TLV [", tlv, "] - Unsupported value type [", tEntity[1], "]", err)
			return
		}
		oP.TLV[libsmpp.TLVCode(tK)] = libsmpp.TLVStruct{
			Data: tV,
			Len:  uint16(len(tV)),
		}
		fmt.Println("TLV [", tlv, "] KEY=", tK, "; VAL[", tV, "]")

	}

	// Encode packet
	rP, rErr := s.EncodeSubmitSm(oP)
	if rErr != nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error encoding packet body")
		return
	}

	// Track message processing time
	TimeTracker := TrackProcessingTime{}

	dest := &net.TCPAddr{IP: remoteIP, Port: int(remotePort)}
	log.WithFields(log.Fields{"type": "smpp-client", "remoteIP": dest}).Info("Connecting to")
	conn, err := net.DialTCP("tcp", nil, dest)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-client", "service": "outConnect", "remoteIP": dest}).Fatal("Cannot connect to")
		return
	}

	go s.RunOutgoing(conn, libsmpp.SMPPBind{
		ConnMode:   cm,
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
				go PacketSender(s, rP, config, &TimeTracker, SendCompleteCH)
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

		case <-s.Closed:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "close"}).Warning("Connection is closed")
			return
		case <-SendCompleteCH:
			s.Close("Send complete")
		}
	}
}

// Send messages
func PacketSender(s *libsmpp.SMPPSession, p libsmpp.SMPPPacket, config Config, TimeTracker *TrackProcessingTime, SendCompleteCH chan interface{}) {
	// Sleep for 3s after finishing sending and close trigger channel
	defer func(config Config) {
		if config.StayConnected {
			return
		} else {
			time.Sleep(5 * time.Second)
			close(SendCompleteCH)
		}
	}(config)

	var tick time.Duration
	var tickCouter uint
	var tickInfoModule uint
	var blockSize uint
	var msgLastSec uint

	// For rate > 1000 SMS/sec send send messages each 2 ms
	if config.SendRate > 1000 {
		tick = 2 * time.Millisecond
		blockSize = config.SendRate / 500
		tickInfoModule = 500
	} else if config.SendRate > 100 {
		// For rate > 100 SMS/sec send send messages each 10 ms
		tick = 10 * time.Millisecond
		blockSize = config.SendRate / 100
		tickInfoModule = 100
	} else if config.SendRate > 10 {
		// For rate > 10 SMS/sec AND <= 100 SMS/sec send messages each 100 ms
		tick = 100 * time.Millisecond
		blockSize = config.SendRate / 10
		tickInfoModule = 10
	} else if config.SendRate > 1 {
		// For rate > 1 SMS/sec AND <= 10 SMS/sec send message each 500 ms
		tick = 500 * time.Millisecond
		blockSize = config.SendRate / 2
		tickInfoModule = 2
	} else {
		// Rate is 1 SMS/sec
		tick = 1 * time.Second
		blockSize = config.SendRate
		tickInfoModule = 1
	}

	var done uint
	c := time.Tick(tick)
	lastInfoReport := time.Now()
	firstInfoReport := time.Now()
	for {
		select {
		case <-c:
			// First tick
			if tickCouter == 0 {
				lastInfoReport = time.Now()
				firstInfoReport = time.Now()
			}

			tickCouter++

			// Init block size for current tick
			tickBlock := blockSize

			// Skip current tick in case of overload
			txQ, rxQ := s.GetTrackQueueSize()
			var skipSend bool
			if config.SendWindow > 0 {
				if uint(txQ) >= config.SendWindow {
					skipSend = true
				}
				if tickBlock+uint(txQ) > config.SendWindow {
					tickBlock = config.SendWindow - uint(txQ)
				}
			}

			var i uint
			if !skipSend {
				for ; (i < tickBlock) && (done < config.SendCount); i++ {
					p.CreateTime = time.Now()
					s.Outbox <- p
					msgLastSec++
					done++
				}
			}
			if done >= config.SendCount {
				reportDiff := time.Since(firstInfoReport).Milliseconds()
				var realRate int64
				if reportDiff > 0 {
					realRate = (int64(config.SendCount) * 1000) / reportDiff
				}

				fmt.Println("#Finished sending", config.SendCount, "messages with expected rate:", config.SendRate, ", real rate:", realRate)
				return
			}

			if tickCouter%tickInfoModule == 0 {
				TimeTracker.Lock()
				tCnt := TimeTracker.Count
				tDur := TimeTracker.DelayTotal
				TimeTracker.Count = 0
				TimeTracker.DelayTotal = 0
				TimeTracker.Unlock()

				var tAvg int64
				if tCnt > 0 {
					if tCnt > 0 {
						tAvg = tDur.Microseconds() / int64(tCnt)
					}
				}

				reportDiff := time.Since(lastInfoReport).Milliseconds()
				if reportDiff < 1 {
					reportDiff = 1
				}
				lastInfoReport = time.Now()
				fmt.Println("[", s.SessionID, "] During last", reportDiff, "ms:", int64(msgLastSec)*1000/reportDiff, "[MAX:", done, "][TX:", txQ, "][RX:", rxQ, "][RTDavg micros: ", tAvg, ",", tCnt, "]")
				msgLastSec = 0
			}

		case <-s.Closed:
			fmt.Println("#ClosedCH")
			return
		}
	}

}
