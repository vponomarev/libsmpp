package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	libsmppConst "github.com/vponomarev/libsmpp/const"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"math/rand"
	"net"
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

	SMPP struct {
		Remote string `yaml:"remote,omitempty"`
		Bind   struct {
			SystemID   string `yaml:"systemID,omitempty"`
			SystemType string `yaml:"systemType,omitempty"`
			Password   string `yaml:"password,omitempty"`
			Mode       string `yaml:"mode,omitempty"`
		}
	}

	Profiler       bool   `yaml:"profiler,omitempty"`
	ProfilerListen string `yaml:"profilerListen,omitempty"`
	Message        struct {
		From struct {
			TON  int    `yaml:"ton"`
			NPI  int    `yaml:"npi"`
			Addr string `yaml:"addr"`
		}
		To struct {
			TON      int    `yaml:"ton"`
			NPI      int    `yaml:"npi"`
			Addr     string `yaml:"addr"`
			Template bool   `yaml:"template"`
		}
		RegisteredDelivery int      `yaml:"registeredDelivery"`
		DataCoding         int      `yaml:"dataCoding"`
		Body               string   `yaml:"body"`
		TLV                []string `yaml:"tlv"`
	}

	SendCount  uint `yaml:"count" envconfig:"SEND_COUNT"`
	SendRate   uint `yaml:"rate" envconfig:"SEND_RATE"`
	SendWindow uint `yaml:"window" envconfig:"SEND_WINDOW"`

	StayConnected bool `yaml:"stayConnected,omitempty"`
}

type Params struct {
	remoteIP   net.IP
	remotePort int
	bindMode   libsmpp.ConnSMPPMode
	submit     libsmpp.SMPPSubmit
}

type TrackProcessingTime struct {
	Count      uint
	DelayMin   time.Duration
	DelayMax   time.Duration
	DelayTotal time.Duration
	sync.RWMutex
}

type TLVDynamic struct {
	ID       libsmpp.TLVCode
	Template string
}

// List of TLV preservation for Delivery Reports
var tlvDynamic []TLVDynamic

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

// Prepare SMPPSubmit structure for stress message generation
func prepareSubmit(config Config) (oP libsmpp.SMPPSubmit, err error) {
	// Prepare SUBMIT_SM packet if specified
	oP = libsmpp.SMPPSubmit{
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
			err = fmt.Errorf("error parsing TLV [%s] - should be 3 params", tlv)
			return
		}

		tVal := strings.Trim(tEntity[2], " ")
		if tVal[0] != '"' || tVal[len(tVal)-1] != '"' {
			err = fmt.Errorf("error parsing TLV [%s] - take value into quotes", tlv)
			return
		}
		tVal = strings.Trim(tVal, "\"")

		var tK int64
		var tV []byte
		if (len(tEntity[0]) > 2) && (tEntity[0][0:2] == "0x") {
			if tK, err = strconv.ParseInt(tEntity[0][2:], 16, 16); err != nil {
				err = fmt.Errorf("error parsing TLV [%s] - HEX key [%s]: %v", tlv, tEntity[0], err)
				return
			}
		} else {
			if tK, err = strconv.ParseInt(tEntity[0], 10, 16); err != nil {
				err = fmt.Errorf("error parsing TLV [%s] - DEC key [%s]: %v", tlv, tEntity[0], err)
				return
			}
		}
		switch strings.Trim(tEntity[1], " ") {
		case "string":
			tV = []byte(tVal)
		case "hex":
			if tV, err = hex.DecodeString(tVal); err != nil {
				err = fmt.Errorf("error parsing TLV [%s] - HEX value [%s]: %v", tlv, tEntity[2], err)
				return
			}
		case "dynamic":
			tlvDynamic = append(tlvDynamic, TLVDynamic{
				ID:       libsmpp.TLVCode(tK),
				Template: tVal,
			})
			fmt.Println("TLV [", tlv, "] KEY=", tK, "; DYNAMIC[", tVal, "]")
			continue
		default:
			err = fmt.Errorf("error parsing TLV [%s] - Unsupported value type [%s]: %v", tlv, tEntity[1], err)
			return
		}
		oP.TLV[libsmpp.TLVCode(tK)] = libsmpp.TLVStruct{
			Data: tV,
			Len:  uint16(len(tV)),
		}
		fmt.Println("TLV [", tlv, "] KEY=", tK, "; VAL[", tV, "]")

	}
	return
}

func main() {
	// Load config file name
	configFileName := flag.String("-config", "config.yml", "Override configuration file name")

	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	// Load configuration file
	config, params, err := loadConfig(*configFileName)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Error loading config file: ", err)
		return
	}

	// Init SMPP Session
	s := &libsmpp.SMPPSession{
		SessionID:   1,
		DebugNetBuf: config.Log.Netbuf,
	}
	s.Init()

	// Init profiler
	runProfiler(s, config)

	// Track message processing time
	TimeTracker := TrackProcessingTime{}

	dest := &net.TCPAddr{IP: params.remoteIP, Port: params.remotePort}
	log.WithFields(log.Fields{"type": "smpp-client", "remoteIP": dest}).Info("Connecting to")
	conn, err := net.DialTCP("tcp", nil, dest)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-client", "service": "outConnect", "remoteIP": dest}).Fatal("Cannot connect to")
		return
	}

	// Init statistics generator
	ctx := context.Background()
	statsLog.Init(ctx)

	go s.RunOutgoing(conn, libsmpp.SMPPBind{
		ConnMode:   params.bindMode,
		SystemID:   config.SMPP.Bind.SystemID,
		Password:   config.SMPP.Bind.Password,
		SystemType: config.SMPP.Bind.SystemType,
		IVersion:   0x34,
	},
		1)

	// Handle `SEND COMPLETE` event
	SendCompleteCH := make(chan interface{})

	//
	lastRXReportTime := time.Now()
	lastRXReportUnix := lastRXReportTime.Unix()
	var lastRXCount uint
	var totalRXCount uint

	for {
		select {
		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "StatusUpdate"}).Warning(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {

				// Start packet submission
				log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "SendPacket", "count": config.SendCount, "rate": config.SendRate}).Info("Start message bulk message submission")
				go PacketSender(s, params.submit, tlvDynamic, config, &TimeTracker, SendCompleteCH)
			}

		case x := <-s.Inbox:
			log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "Inbox"}).Debug(x)

			// Generate confirmation for DeliverSM
			if x.Hdr.ID == libsmppConst.CMD_DELIVER_SM {
				// Calculate number of received messages per second
				if time.Now().Unix() > lastRXReportUnix {
					statsLog.Update(time.Now(), StatCounter{{ID: "RecvRate", Value: uint32(lastRXCount)}, {ID: "RecvCount", Value: uint32(totalRXCount)}})
					lastRXReportTime = time.Now()
					lastRXReportUnix = lastRXReportTime.Unix()
					lastRXCount = 0
				}

				lastRXCount++
				totalRXCount++
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

			// Sleep 500ms to complete Ring Buffer print process
			time.Sleep(500 * time.Millisecond)
			return
		case <-SendCompleteCH:
			s.Close("Send complete")
		}
	}
}

// Send messages
func PacketSender(s *libsmpp.SMPPSession, ps libsmpp.SMPPSubmit, tlvDynamic []TLVDynamic, config Config, TimeTracker *TrackProcessingTime, SendCompleteCH chan interface{}) {
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

	// Encode packet for static generation
	p, rErr := s.EncodeSubmitSm(ps)
	if rErr != nil {
		log.WithFields(log.Fields{"type": "smpp-client", "service": "packetSender"}).Fatal("Error encoding static packet")
		return
	}

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

				// Don't sent anything during first tick
				tickCouter++
				continue
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

					// Dynamically generate message if:
					// - TO is templated
					// - There're TLV Dynamic fields
					if config.Message.To.Template || (len(tlvDynamic) > 0) {
						// Prepare new packet instance
						pN := ps

						// Process TO number template
						if config.Message.To.Template {
							randRunes := []rune("0123456789")

							// Activate replacement only in case of template data
							if cRnd := strings.Count(config.Message.To.Addr, "#"); cRnd > 0 {
								da := config.Message.To.Addr
								for ; cRnd > 0; cRnd-- {
									da = strings.Replace(da, "#", string(randRunes[rand.Intn(len(randRunes))]), 1)
								}
								pN.Dest.Addr = da
							}
						}

						// Add DYNAMIC TLV fields
						for _, dV := range tlvDynamic {
							tData := dV.Template
							if strings.Contains(tData, "{timestamp}") {
								tData = strings.ReplaceAll(tData, "{timestamp}", strconv.FormatInt(time.Now().Unix(), 10))
							}

							pN.TLV[dV.ID] = libsmpp.TLVStruct{
								Data: []byte(tData),
								Len:  uint16(len(tData)),
							}
						}

						// Encode SubmitSM packet
						var rErr error
						p, rErr = s.EncodeSubmitSm(pN)
						if rErr != nil {
							log.WithFields(log.Fields{"type": "smpp-client", "service": "packetSender"}).Fatal("Error encoding packet body in message loop")
							return
						}

					}
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
				statsLog.Update(lastInfoReport, StatCounter{{ID: "SentRate", Value: uint32(int64(msgLastSec) * 1000 / reportDiff)}, {ID: "SentCount", Value: uint32(done)}, {ID: "SentRTD", Value: uint32(tAvg / 1000)}})

				msgLastSec = 0
			}

		case <-s.Closed:
			fmt.Println("#ClosedCH")
			return
		}
	}

}
