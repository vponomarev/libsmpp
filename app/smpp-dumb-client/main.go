package main

import (
	"context"
	"flag"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	libsmppConst "github.com/vponomarev/libsmpp/const"
	"net"
	_ "net/http/pprof"
	"os"
	"time"
)

// List of TLV preservation for Delivery Reports
var tlvDynamic []TLVDynamic

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

				// Start packet submission IF enabled
				if config.Generator.Enabled {
					log.WithFields(log.Fields{"type": "smpp-client", "SID": s.SessionID, "service": "outConnect", "action": "SendPacket", "count": config.Generator.SendCount, "rate": config.Generator.SendRate}).Info("Start message bulk message submission")
					go PacketSender(s, params.submit, tlvDynamic, config, &TimeTracker, SendCompleteCH)
				}
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
