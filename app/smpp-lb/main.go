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

// FLAG: Stop handling traffic
var stopCh chan struct{}

type Config struct {
	Server struct {
		Port int
	}
	Logging struct {
		Server struct {
			Rate bool
		}
	}
}

func doStop() bool {
	select {
	case <-stopCh:
		return true
	default:
		close(stopCh)
	}
	return false
}

func hConn(id uint32, conn *net.TCPConn, pool *libsmpp.SessionPool, config Config) {

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

	if config.Logging.Server.Rate {

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
	}
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
				pool.RegisterSession(s)
				return
			}

		case <-s.Closed:
			fmt.Println("[", id, "] Connection is closed!")
			return
		}
	}

}

func main() {
	//	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	log.WithFields(log.Fields{
		"type": "smpp-lb",
	}).Info("Start")

	// Load configuration file
	config := Config{}

	configFileName := "config.yml"
	source, err := ioutil.ReadFile(configFileName)
	if err == nil {
		if err = yaml.Unmarshal(source, &config); err == nil {
			log.WithFields(log.Fields{
				"type": "smpp-server",
			}).Info("Loaded configuration file: ", configFileName)
		} else {
			fmt.Println("Error loading config file: ", err)
			return
		}
	}

	// Fill default values for config
	if config.Server.Port < 1 {
		config.Server.Port = 2775
	}

	pool := libsmpp.SessionPool{}
	pool.Init()

	var id uint32 = 1

	go outConnect(id, &pool)

	// Listen socket for new connections
	lAddr := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: config.Server.Port}

	socket, err := net.ListenTCP("tcp4", lAddr)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-lb"}).Error("Error listening TCP socket: ", err)
		return
	}
	log.WithFields(log.Fields{"type": "smpp-lb", "service": "ListenTCP"}).Warning("Starting listening on: ", socket.Addr().String())

	for {
		conn, err := socket.AcceptTCP()
		id++
		if err != nil {
			log.WithFields(log.Fields{"type": "smpp-lb"}).Error("Error accepting socket connection: ", err)
			return
		}
		log.WithFields(log.Fields{"type": "smpp-lb", "remoteIP": conn.RemoteAddr().String()}).Warning("Received incoming conneciton")
		go hConn(id, conn, &pool, config)
	}
}

func outConnect(id uint32, pool *libsmpp.SessionPool) {
	dest := &net.TCPAddr{IP: net.IPv4(172, 21, 211, 199), Port: 2775}
	conn, err := net.DialTCP("tcp", nil, dest)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-lb", "service": "outConnect", "remoteIP": dest}).Warning("Cannot connect to")
		return
	}

	s := &libsmpp.SMPPSession{
		SessionID: id,
	}
	s.Init()

	go s.RunOutgoing(conn, libsmpp.SMPPBind{
		ConnMode:   libsmpp.CSMPPTRX,
		SystemID:   "test_dp",
		Password:   "test12",
		SystemType: "",
		IVersion:   0,
		AddrTON:    0,
		AddrNPI:    0,
		AddrRange:  "",
	},
		id)

	for {
		select {
		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "smpp-lb", "SID": s.SessionID, "service": "outConnect", "action": "StatusUpdate"}).Warning(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {
				// Pass session to SessionPool
				pool.RegisterSession(s)
				return
			}

		case <-s.Closed:
			log.WithFields(log.Fields{"type": "smpp-lb", "SID": s.SessionID, "service": "outConnect", "action": "close"}).Warning("Connection is closed")
			return
		}
	}
}
