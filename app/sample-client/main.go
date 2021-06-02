package main

import (
	"fmt"
	"github.com/vponomarev/libsmpp"
	"net"
	_ "net/http/pprof"
)

func main() {
	// Connection params
	remoteIP := net.ParseIP("127.0.0.1")
	remotePort := 2500

	// Init SMPP Session
	s := &libsmpp.SMPPSession{
		SessionID: 1,
	}
	s.Init()

	fmt.Print("Connecting to ", remoteIP, remotePort)

	dest := &net.TCPAddr{IP: remoteIP, Port: remotePort}
	conn, err := net.DialTCP("tcp", nil, dest)
	if err != nil {
		fmt.Println("cannot connect:", err)
		return
	}

	go s.RunOutgoing(conn, libsmpp.SMPPBind{
		ConnMode:   libsmpp.CSMPPTRX,
		SystemID:   "test",
		Password:   "test",
		SystemType: "test",
		IVersion:   0x34,
	},
		1)

	state, err := s.SyncBindWait()
	fmt.Println("Bind state: ", state, err)

}
