package main

import (
	"fmt"
	"libsmpp"
	"net"
	"time"
)

// FLAG: Stop handling traffic
var stopCh chan struct{}

func doStop() bool {
	select {
	case <-stopCh:
		return true
	default:
		close(stopCh)
	}
	return false
}

func hConn(id uint16, conn *net.TCPConn, pool *libsmpp.SessionPool) {
	//	defer conn.Close()

	// Allocate new SMPP Session structure
	s := &libsmpp.SMPPSession{ManualBindValidate: true, DebugLevel: 1}
	s.Init()

	go s.RunIncoming(conn, id)

	var msgID uint
	msgID = 1

	go func(id uint16, msgID *uint, s *libsmpp.SMPPSession) {
		var sv uint
		sv = 0
		c := time.Tick(1000 * time.Millisecond)
		for {

			select {
			case <-c:
				sn := *msgID
				if sn > sv {
					fmt.Println("[", id, "] During last 1s: ", (sn - sv))
					sv = sn
				} else {
					fmt.Println("[", id, "] During last 1s: -")
				}
			case <-s.Closed:
				return
			}
		}
	}(id, &msgID, s)

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
	fmt.Println("Go start!")
	pool := libsmpp.SessionPool{}
	pool.Init()

	var id uint16 = 1

	go outConnect(id, &pool)

	// Listen socket for new connections
	lAddr := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 2775}

	socket, err := net.ListenTCP("tcp4", lAddr)
	if err != nil {
		fmt.Println("Error listening TCP socket:", err)
		return
	}
	fmt.Println("Starting listening on: ", socket.Addr().String())

	for {
		conn, err := socket.AcceptTCP()
		id++
		if err != nil {
			fmt.Println("Error accepting socket connection:", err)
			return
		}
		fmt.Println("[", id, "] Incoming connection from: ", conn.RemoteAddr().String())
		go hConn(id, conn, &pool)
	}
}

func outConnect(id uint16, pool *libsmpp.SessionPool) {
	dest := &net.TCPAddr{IP: net.IPv4(172, 21, 211, 199), Port: 2775}
	conn, err := net.DialTCP("tcp", nil, dest)
	if err != nil {
		fmt.Printf("Cannot connect to [%s]\n", dest)
		return
	}

	s := &libsmpp.SMPPSession{}
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
			fmt.Println("[", id, "] ## StatusUpdate: ", x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
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
