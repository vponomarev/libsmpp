package main

import (
	"encoding/hex"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	libsmppConst "github.com/vponomarev/libsmpp/const"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	RESP_MSGID_HEX = iota + 1
	RESP_MSGID_UUID
)

type Config struct {
	Listen int `yaml:"listen,omitempty"`
	Log    struct {
		Level  string `yaml:"level,omitempty"`
		Rate   bool
		Netbuf bool `yaml:"netbuf"`
	}

	Responder struct {
		MsgID string `yaml:"msgid",omitempty`
		Delay struct {
			Min int `yaml:"min,omitempty"`
			Max int `yaml:"max,omitempty"`
		}
	}

	Deliveryreport struct {
		Enabled bool     `yaml:"enabled"`
		TLV     []string `yaml:"tlv"`
		Delay   struct {
			Min int `yaml:"min,omitempty"`
			Max int `yaml:"max,omitempty"`
		}
	}
}

// Loaded config
type LConfig struct {
	MsgidFormat int
}

type Params struct {
	LogLevel log.Level
	Flags    struct {
		LogLevel bool
	}
}

var tlvList []libsmpp.TLVInfo

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
	// Init random seed
	rand.Seed(time.Now().UnixNano())

	pParam := ProcessCMDLine()

	fmt.Println("LogLevel:", pParam.LogLevel.String())
	log.SetOutput(os.Stdout)
	log.SetLevel(pParam.LogLevel)

	log.WithFields(log.Fields{
		"type": "smpp-server",
	}).Info("Start")

	// Load configuration file
	var config Config
	var lConfig LConfig

	configFileName := "config.yml"
	source, err := ioutil.ReadFile(configFileName)
	if err == nil {
		if err = yaml.Unmarshal(source, &config); err == nil {
			log.WithFields(log.Fields{
				"type": "smpp-server",
			}).Info("Loaded configuration file: ", configFileName)
		} else {
			log.WithFields(log.Fields{
				"type": "smpp-server",
			}).Fatal("Error parsing config file")
			return
		}
	}

	// Load LogLevel from config if present
	if (len(config.Log.Level) > 0) && (!pParam.Flags.LogLevel) {
		if l, err := log.ParseLevel(config.Log.Level); err == nil {
			pParam.LogLevel = l

			log.SetLevel(pParam.LogLevel)
			log.WithFields(log.Fields{
				"type": "smpp-server",
			}).Warning("Override LogLevel to: ", pParam.LogLevel.String())
		}
	}

	if len(config.Responder.MsgID) > 0 {
		switch config.Responder.MsgID {
		case "hex":
			lConfig.MsgidFormat = RESP_MSGID_HEX
		case "uuid":
			lConfig.MsgidFormat = RESP_MSGID_UUID
		default:
			log.WithFields(log.Fields{
				"type": "smpp-server",
			}).Fatal("Incorrect value for configuration param responder.msgidFormat [", config.Responder.MsgID, "]")
			return
		}
	} else {
		lConfig.MsgidFormat = RESP_MSGID_HEX
	}

	// Preload Receipt TLV values
	for _, tlv := range config.Deliveryreport.TLV {
		tEntity := strings.Split(tlv, ";")
		if len(tEntity) != 3 {
			log.WithFields(log.Fields{"type": "smpp-server"}).Fatal("Error parsing TLV [", tlv, "] - should be 3 params")
			return
		}

		tVal := strings.Trim(tEntity[2], " ")
		if tVal[0] != '"' || tVal[len(tVal)-1] != '"' {
			log.WithFields(log.Fields{"type": "smpp-server"}).Fatal("Error parsing TLV [", tlv, "] - take value into quotes")
			return
		}
		tVal = strings.Trim(tVal, "\"")

		var tK int64
		var tV []byte
		var err error
		if (len(tEntity[0]) > 2) && (tEntity[0][0:2] == "0x") {
			if tK, err = strconv.ParseInt(tEntity[0][2:], 16, 16); err != nil {
				log.WithFields(log.Fields{"type": "smpp-server"}).Fatal("Error parsing TLV [", tlv, "] - HEX key [", tEntity[0], "]: ", err)
				return
			}
		} else {
			if tK, err = strconv.ParseInt(tEntity[0], 10, 16); err != nil {
				log.WithFields(log.Fields{"type": "smpp-server"}).Fatal("Error parsing TLV [", tlv, "] - HEX key [", tEntity[0], "]: ", err)
				return
			}
		}
		switch strings.Trim(tEntity[1], " ") {
		case "string":
			tV = []byte(tVal)
		case "hex":
			if tV, err = hex.DecodeString(tVal); err != nil {
				log.WithFields(log.Fields{"type": "smpp-server"}).Fatal("Error parsing TLV [", tlv, "] - HEX value [", tEntity[2], "]: ", err)
				return
			}
		default:
			log.WithFields(log.Fields{"type": "smpp-server"}).Fatal("Error parsing TLV [", tlv, "] - Unsupported value type [", tEntity[1], "]", err)
			return
		}

		tlvList = append(tlvList, libsmpp.TLVInfo{
			ID: libsmpp.TLVCode(tK),
			Data: libsmpp.TLVStruct{
				Data: tV,
				Len:  uint16(len(tV)),
			},
		})
		fmt.Println("TLV [", tlv, "] KEY=", tK, "; VAL[", tV, "]")
	}

	// Fill default values for config
	if config.Listen < 1 {
		config.Listen = 2775
	}
	// Listen socket for new connections
	lAddr := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: config.Listen}

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
		go hConn(id, conn, &config, &lConfig)
	}
}

func hConn(id uint32, conn *net.TCPConn, config *Config, lConfig *LConfig) {
	// Allocate new SMPP Session structure
	s := &libsmpp.SMPPSession{
		ManualBindValidate: true,
		DebugLevel:         1,
		SessionID:          id,
		DebugNetBuf:        config.Log.Netbuf,
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
				// Pass session to session processor
				go sessionProcessor(s, config, lConfig)
			}

		case <-s.Closed:
			fmt.Println("[", id, "] Connection is closed!")
			return
		}
	}
}

func sessionProcessor(s *libsmpp.SMPPSession, config *Config, lConfig *LConfig) {
	var msgID uint32
	msgID = 1

	log.WithFields(log.Fields{"type": "smpp-server", "service": "PacketLoop", "SID": s.SessionID, "action": "Start"}).Info("Start message processing")

	if config.Log.Rate {
		go func(msgID *uint32, s *libsmpp.SMPPSession) {
			var sv uint32
			sv = 0
			c := time.Tick(1000 * time.Millisecond)
			for {

				select {
				case <-c:
					sn := *msgID
					if sn > sv {
						tx, rx := s.GetTrackQueueSize()
						fmt.Println("[", s.SessionID, "] During last 1s: ", (sn - sv), " [MAX:", sn, "][TX:", tx, "][RX:", rx, "]")
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
			dMsgID := msgID

			var rMsgID string
			if lConfig.MsgidFormat == RESP_MSGID_HEX {
				// HEX
				rMsgID = fmt.Sprintf("%06x", msgID)
			} else {
				// UUID
				rMsgID = fmt.Sprintf("LibSMPP-SRV-%016d", msgID)
			}

			pR := s.EncodeSubmitSmResp(p, 0, rMsgID)

			// Generate response
			if (config.Responder.Delay.Min > 0) || (config.Responder.Delay.Max > 0) {
				// Delayed response
				msgDelayDelta := config.Responder.Delay.Max - config.Responder.Delay.Min
				if msgDelayDelta <= 0 {
					msgDelayDelta = 0
				} else {
					msgDelayDelta = rand.Intn(msgDelayDelta)
				}

				go func(s *libsmpp.SMPPSession, pR libsmpp.SMPPPacket, d time.Duration) {
					select {
					case <-time.After(d):
						s.Outbox <- pR
					case <-s.Closed:
					}
				}(s, pR, time.Duration(config.Responder.Delay.Max+msgDelayDelta)*time.Millisecond)
			} else {
				// Instant response
				s.Outbox <- pR
			}
			msgID++

			if config.Deliveryreport.Enabled {

				var msgDelayDelta int
				if (config.Responder.Delay.Min > 0) || (config.Responder.Delay.Max > 0) {
					// Delayed response
					msgDelayDelta = config.Responder.Delay.Max - config.Responder.Delay.Min
					if msgDelayDelta <= 0 {
						msgDelayDelta = 0
					} else {
						msgDelayDelta = rand.Intn(msgDelayDelta)
					}
				}

				// Generate DELIVERY REPORT in a separate go thread
				go func(p libsmpp.SMPPPacket, dMsgID uint32, rMsgID string, state uint8, d time.Duration) {
					pD, err := s.DecodeSubmitDeliverSm(&p)
					if err != nil {
						return
					}
					// SWAP Source <=> Dest
					ax := pD.Source
					pD.Source = pD.Dest
					pD.Dest = ax

					// Fill Message Text
					tText := (time.Now()).Format("0601021504")
					pD.ShortMessages = fmt.Sprintf("id:%010d sub:001 dlvrd:001 submit date:%10s done date:%10s stat:%7s err:%3s text:", dMsgID, tText, tText, libsmpp.StateName(state), "000")
					pD.TLV = make(map[libsmpp.TLVCode]libsmpp.TLVStruct)

					// === TLV ===
					// Predefined fields
					for _, v := range tlvList {
						pD.TLV[v.ID] = v.Data
					}

					// 0x001e Receipted message ID
					pD.TLV[0x1e] = libsmpp.TLVStruct{
						Data: []byte(rMsgID),
						Len:  uint16(len(rMsgID)),
					}

					// 0x0427 Message state
					pD.TLV[0x0427] = libsmpp.TLVStruct{
						Data: []byte{state},
						Len:  1,
					}
					pE, err := s.EncodeDeliverSm(pD)
					if err != nil {
						return
					}

					if d > 0 {
						// Delayed report
						select {
						case <-time.After(d):
							s.Outbox <- pE
						case <-s.Closed:
						}
					} else {
						// Instant report
						s.Outbox <- pE
					}
				}(p, dMsgID, rMsgID, libsmppConst.STATE_REJECTED, time.Duration(config.Deliveryreport.Delay.Max+msgDelayDelta)*time.Millisecond)
			}

		case p := <-s.InboxR:
			log.WithFields(log.Fields{"type": "smpp-server", "service": "PacketLoopR", "SID": s.SessionID, "action": fmt.Sprintf("%x (%s)", p.Hdr.ID, libsmpp.CmdName(p.Hdr.ID)), "Seq": p.Hdr.Seq, "Len": p.Hdr.Len}).Trace(fmt.Sprintf("%x", p.Body))

		case <-s.Closed:
			return
		}
	}
}
