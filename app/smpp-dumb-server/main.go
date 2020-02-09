package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"libsmpp"
	"net"
	"os"
	"time"
)

func main() {
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	log.WithFields(log.Fields{
		"type": "smpp-server",
	}).Info("Start")

	// Listen socket for new connections
	lAddr := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 2775}

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
			fmt.Println("[", id, "] ## StatusUpdate: ", x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
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

	for {
		select {
		case x := <-s.Inbox:
			fmt.Println("[", s.SessionID, "] Incoming packet: ", x)
		case <-s.Closed:
			return
		}
	}
}
