package main

import (
	"encoding/hex"
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"libsmpp"
	libsmpp2 "libsmpp/const"
	"net"
	"os"
	"strconv"
	"strings"
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
	Responder struct {
		DeliveryReport bool     `yaml:"deliveryReport"`
		TLV            []string `yaml:"tlv"`
	}
	DebugNetBuf bool `yaml:"debugNetBuf"`
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

	// Preload Receipt TLV values
	for _, tlv := range config.Responder.TLV {
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
		DebugNetBuf:        config.DebugNetBuf,
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
				go sessionProcessor(s, config)
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

	log.WithFields(log.Fields{"type": "smpp-server", "service": "PacketLoop", "SID": s.SessionID, "action": "Start"}).Info("Start message processing")

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
			rMsgID := fmt.Sprintf("%06x", msgID)
			dMsgID := msgID

			pR := s.EncodeSubmitSmResp(p, 0, rMsgID)
			s.Outbox <- pR
			msgID++

			if config.Responder.DeliveryReport {
				// Generate DELIVERY REPORT in a separate go thread
				go func(p libsmpp.SMPPPacket, dMsgID uint32, rMsgID string, state uint8) {
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
					msgIdHex := fmt.Sprintf("%09x", dMsgID)
					pD.TLV[0x1e] = libsmpp.TLVStruct{
						Data: []byte(msgIdHex),
						Len:  uint16(len(msgIdHex)),
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

					s.Outbox <- pE
				}(p, dMsgID, rMsgID, libsmpp2.STATE_REJECTED)
			}

		case p := <-s.InboxR:
			log.WithFields(log.Fields{"type": "smpp-server", "service": "PacketLoopR", "SID": s.SessionID, "action": fmt.Sprintf("%x (%s)", p.Hdr.ID, libsmpp.CmdName(p.Hdr.ID)), "Seq": p.Hdr.Seq, "Len": p.Hdr.Len}).Trace(fmt.Sprintf("%x", p.Body))

		case <-s.Closed:
			return
		}
	}
}
