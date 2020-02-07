package libsmpp

import (
	"encoding/binary"
	"fmt"
	"io"
	"libsmpp/const"
	"net"
	"time"
)

//
// Init SMPP Session
func (s *SMPPSession) Init() {
	// Direction: Unknown
	s.Cs.cd = CDirUndefined

	// TCP State: Undefined
	s.Cs.ts = CTCPUndefined

	// SMPP State
	s.Cs.ss = CSMPPIdle

	// Connection state channel
	s.Status = make(chan ConnState)

	s.Closed = make(chan interface{})

	s.Inbox = make(chan SMPPPacket)
	s.InboxR = make(chan SMPPPacket)
	s.Outbox = make(chan SMPPPacket)
	s.OutboxRAW = make(chan []byte)

	if s.ManualBindValidate {
		s.BindValidator = make(chan BindValidatorRequest)
		s.BindValidatorR = make(chan BindValidatorResponce)
	}

	// Set default timeouts for TX/RX packets
	if s.TXMaxTimeoutMS == 0 {
		s.TXMaxTimeoutMS = libsmpp.TX_MAX_TIMEOUT_MS
	}

	if s.RXMaxTimeoutMS == 0 {
		s.RXMaxTimeoutMS = libsmpp.RX_MAX_TIMEOUT_MS
	}

	// Allocate map for packet tracking
	s.TrackRX = make(map[uint32]SMPPTracking, 100)
	s.TrackTX = make(map[uint32]SMPPTracking, 100)
}

func (s *SMPPSession) reportStateS(q connState, ierr error, inerr error) {
	// cd, ts, ss, sm
	if q.cd != CDirUndefined {
		s.Cs.cd = q.cd
	}
	if q.ts != CTCPUndefined {
		s.Cs.ts = q.ts
	}
	if q.ss != CSMPPIdle {
		s.Cs.ss = q.ss
	}
	if q.sm != CSMPPUndefined {
		s.Cs.sm = q.sm
	}
	s.Cs.err = ierr
	s.Cs.nerr = inerr

	s.Status <- &s.Cs
}

// Timeout handler for BIND
func (s *SMPPSession) bindTimeouter(t int) {
	select {
	case <-time.After(time.Duration(t) * time.Second):
		if s.Cs.GetSMPPState() != CSMPPBound {
			fmt.Println("bindTimeouter(", t, ") Event triggered!")
			s.conn.Close()
		}
	case <-s.Closed:
		return
	}
}

// Packet expiration tracker
func (s *SMPPSession) trackPacketTimeout() {
	tk := time.NewTicker(500 * time.Millisecond)
	select {
	case <-tk.C:
		// TODO
		// 1. Search for expired packets
		s.winMutex.RLock()
		for k, v := range s.TrackRX {
			if time.Since(v.T) > time.Duration(s.RXMaxTimeoutMS)*time.Millisecond {
				fmt.Println("#", k, " - Expired RX packet")
			}
		}
		for k, v := range s.TrackTX {
			if time.Since(v.T) > time.Duration(s.TXMaxTimeoutMS)*time.Millisecond {
				fmt.Println("#", k, " - Expired RX packet")
			}
		}
		s.winMutex.RUnlock()

	case <-s.Closed:
		return
	}
}

// Allocate SEQUENCE number
func (s *SMPPSession) allocateSeqNo() uint32 {
	s.seqMTX.RLock()
	s.LastTXSeq++
	x := s.LastTXSeq
	s.seqMTX.RUnlock()
	return x
}

func (s *SMPPSession) enquireSender(t int) error {
	fmt.Println("#EnquireSender (", t, "): started ENQUIRE_LINK generator")
	tk := time.NewTicker(time.Duration(t) * time.Second)
	for {
		select {
		case <-tk.C:
			// Send outgoing ENQUIRE_LINK packet
			s.OutboxRAW <- s.EncodeEnquireLinkRAW(s.allocateSeqNo())
		case <-s.Closed:
			return nil
		}
	}
	return nil
}

func (s *SMPPSession) enquireResponder(p *SMPPPacket, seq uint32) {
	s.OutboxRAW <- s.EncodeEnquireLinkRespRAW(p.Hdr.Seq)
}

// Packet REQ/RESP tracking engine
// dir ConnDirection:
// * CDirOutgoing - packets, that are sent to socket
// * CDirIncoming - packets. that are received from socket
// flagDropPacket - FLAG if packet shouldn't be delivered to recipient, because it is already delivered by timeout or this is wrong packet
func (s *SMPPSession) winTrackEvent(dir ConnDirection, p *SMPPPacket) (flagDropPacket bool) {
	// Lock mutex
	s.winMutex.RLock()

	// Process CommandID
	switch p.Hdr.ID {
	case libsmpp.CMD_SUBMIT_SM, libsmpp.CMD_DELIVER_SM, libsmpp.CMD_QUERY_SM, libsmpp.CMD_REPLACE_SM, libsmpp.CMD_CANCEL_SM:
		if dir == CDirIncoming {
			s.RXWindow++

			// Register new tracking message
			s.TrackRX[p.Hdr.Seq] = SMPPTracking{
				SeqNo:     p.Hdr.Seq,
				CommandID: p.Hdr.ID,
				T:         time.Now(),
			}
		}
		if dir == CDirOutgoing {
			s.TXWindow++

			// Register new tracking message
			s.TrackTX[p.Hdr.Seq] = SMPPTracking{
				SeqNo:               p.Hdr.Seq,
				CommandID:           p.Hdr.ID,
				T:                   time.Now(),
				UplinkTransactionID: p.UplinkTransactionID,
			}
			fmt.Println("winTrackEvent(", dir, ")# TrackTX[", p.Hdr.Seq, "]: set UplinkID=", p.UplinkTransactionID)
		}
	case libsmpp.CMD_SUBMIT_SM_RESP, libsmpp.CMD_DELIVER_SM_RESP, libsmpp.CMD_QUERY_SM_RESP, libsmpp.CMD_REPLACE_SM_RESP, libsmpp.CMD_CANCEL_SM_RESP:
		if dir == CDirIncoming {
			s.TXWindow--
			if x, ok := s.TrackTX[p.Hdr.Seq]; ok {
				// Preserve UplingTransactionID for reply packet
				p.UplinkTransactionID = x.UplinkTransactionID
				fmt.Println("winTrackEvent(", dir, ")# TrackTX[", p.Hdr.Seq, "]: get UplinkID:", x.UplinkTransactionID)

				// Remove tracking of sent packet
				delete(s.TrackTX, p.Hdr.Seq)
			} else {
				// Received unhandled _RESP packet
				fmt.Println("winTrackEvent(", dir, ")# TrackTX[", p.Hdr.Seq, "] IS LOST")

				// Mark, that packet will be duplicated
				flagDropPacket = true
			}
		}
		if dir == CDirOutgoing {
			s.RXWindow--
			if _, ok := s.TrackRX[p.Hdr.Seq]; ok {
				// Remove tracking of received packet
				delete(s.TrackRX, p.Hdr.Seq)
			} else {
				fmt.Println("winTrackEvent# Incoming # Received untracked Seq=", p.Hdr.Seq, "")

				// Mark, that packet will be duplicated
				flagDropPacket = true
			}
		}
	}
	s.winMutex.RUnlock()

	return
}

// Take messages from outbox and send messages to the wire
func (s *SMPPSession) processOutbox() {
	fmt.Println("# Started OutBox processor")
	for {
		select {
		case p := <-s.Outbox:
			// Send packet: Encode data from SMPPPacket.HDR + body from SMPPPacket.Body
			buf := make([]byte, 16+p.BodyLen)

			binary.BigEndian.PutUint32(buf, 16+p.BodyLen)
			binary.BigEndian.PutUint32(buf[4:], p.Hdr.ID)
			binary.BigEndian.PutUint32(buf[8:], p.Hdr.Status)
			if p.SeqComplete {
				binary.BigEndian.PutUint32(buf[12:], p.Hdr.Seq)
			} else {
				p.Hdr.Seq = s.allocateSeqNo()
				binary.BigEndian.PutUint32(buf[12:], p.Hdr.Seq)
			}
			if p.BodyLen > 0 {
				copy(buf[16:], p.Body[0:p.BodyLen])
			}

			// Track WINDOW
			if s.winTrackEvent(CDirOutgoing, &p) {
				// Don't send anything to network if FlagDropPacket is set, because confirmation was already
				// sent by timeout OR this packet has wrong SeqNumber in response
				break
			}

			// Send traffic into socket
			n, err := s.conn.Write(buf)
			if err != nil {
				// BREAK CONNECTION
				fmt.Println("#> processOutbox() - error writing Outbox [", n, "bytes]")
				return
			}
			if s.DebugLevel > 1 {
				fmt.Println("#> outBox sent ", n, "bytes")
			}
		case p := <-s.OutboxRAW:
			// Send RAW packet from SMPPPacket.Body
			n, err := s.conn.Write(p)
			if err != nil {
				// BREAK CONNECTION
				fmt.Println("#> processOutboxRAW() - error writing Outbox [", n, "bytes]")
				return
			}
			if s.DebugLevel > 1 {
				fmt.Println("#> outBoxRAW sent ", n, "bytes")
			}

		case <-s.Closed:
			fmt.Println("# Closing outbox processor")
			return
		}
	}

}

//
func (s *SMPPSession) RunOutgoing(conn *net.TCPConn, b SMPPBind, id uint16) {
	s.Run(conn, CDirOutgoing, b, id)
}

func (s *SMPPSession) RunIncoming(conn *net.TCPConn, id uint16) {
	s.Run(conn, CDirIncoming, SMPPBind{}, id)
}

//
func (s *SMPPSession) Run(conn *net.TCPConn, cd ConnDirection, cb SMPPBind, id uint16) {
	defer conn.Close()
	defer close(s.Closed)

	s.conn = conn // Connection socket

	switch cd {
	case CDirIncoming:
		s.Cs.cd = CDirIncoming     // Manage connection direction
		s.Cs.ts = CTCPIncoming     // TCP: Incoming
		s.Cs.ss = CSMPPWaitForBind // SMPP: Wait for bind
	case CDirOutgoing:
		s.Cs.cd = CDirOutgoing  // Manage connection direction
		s.Cs.ts = CTCPOutgoing  // TCP: Outgoing
		s.Cs.ss = CSMPPBindSent // SMPP: Bind sent
	default:
		s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Invalid connection direction"), nil)
		return
	}

	// Run bind timeout tracked
	go s.bindTimeouter(5)

	// Run background OutBox processor
	go s.processOutbox()

	// Allocate new packet
	p := &SMPPPacket{}

	// Waiting for incoming data
	hdrBuf := make([]byte, 16)
	buf := make([]byte, MaxSMPPPacketSize)

	// Send BIND request for outgoing connections
	if cd == CDirOutgoing {
		b, err := s.EncodeBind(cb.ConnMode, cb)
		if err != nil {
			s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Unable to encode bind request!"), err)
			return
		}
		s.Outbox <- b
	}

	// Message processing loop
	for {
		// Read packet header
		if _, err := io.ReadFull(conn, hdrBuf); err != nil {
			s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Error reading incoming packet header (16 bytes)!"), err)
			return
		}
		// Decode header
		if err := p.DecodeHDR(hdrBuf); err != nil {
			s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Error decoding header!"), err)
			return
		}

		// Validate SMPP packet size
		if p.Hdr.Len > MaxSMPPPacketSize {
			// Invalid packet. Break
			s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Incoming packet is too large (%d, while MaxPacketSize is %d)", p.Hdr.Len, MaxSMPPPacketSize), nil)
			return
		}

		// Read least part of the packet
		if p.Hdr.Len > 16 {
			if _, err := io.ReadFull(conn, buf[0:p.Hdr.Len-16]); err != nil {
				s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Error reading last part of the packet!"), err)
				return
			}
		}
		if s.DebugLevel > 2 {
			fmt.Printf("[ %d ][%d] libsmpp: Received CMD: [%x (%s)][size: %d][%x]\n", id, p.Hdr.Seq, p.Hdr.ID, libsmpp.CmdName(p.Hdr.ID), p.Hdr.Len, buf)
		}

		// Fill packet body
		p.BodyLen = p.Hdr.Len - 16
		p.Body = make([]byte, p.BodyLen)
		copy(p.Body, buf[0:p.BodyLen])

		// Handle incoming command
		switch p.Hdr.ID {
		// =============================================================
		// BIND RECEIVER/TRANSMITTER/TRANSCIEVER
		case libsmpp.CMD_BIND_RECEIVER, libsmpp.CMD_BIND_TRANSMITTER, libsmpp.CMD_BIND_TRANSCIEVER:
			// Check bind state
			if s.Cs.ss != CSMPPWaitForBind {
				// Invalid bind state
				s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Received BIND request, while session is in %d (%s) state", s.Cs.GetSMPPState(), s.Cs.GetSMPPState().String()), nil)
				// Generate INVALID_BIND_STATE and close
				b := s.EncodeBindRespRAW(p.Hdr.ID, p.Hdr.Seq, libsmpp.ESME_RINVBNDSTS, "GO-SMPP")
				s.OutboxRAW <- b

				s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Received BIND packet in state %d (%s)!", s.Cs.GetSMPPState(), s.Cs.GetSMPPState().String()), nil)
				return
			}

			// Handle BIND request
			if erx := s.DecodeBind(p); erx == nil {
				if s.DebugLevel > 1 {
					fmt.Println("Incoming BIND request")
					fmt.Printf("SystemID: [%s]\n", s.Bind.SystemID)
					fmt.Printf("Password: [%s]\n", s.Bind.Password)
					fmt.Printf("System Type: [%s]\n", s.Bind.SystemType)
					fmt.Printf("Interface version: [%d]\n", s.Bind.IVersion)
				}
			} else {
				s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Cannot decode incoming BIND packet!"), erx)
				return
			}

			// MANUAL BIND VALIDATION
			if s.ManualBindValidate {
				// Prepare validator request
				bvr := BindValidatorRequest{
					Bind: s.Bind,
					ID:   id,
				}
				// Send validator request
				s.BindValidator <- bvr

				// Wait for response
				select {
				case r := <-s.BindValidatorR:
					b := s.EncodeBindRespRAW(p.Hdr.ID, p.Hdr.Seq, r.Status, r.SMSCID)
					s.OutboxRAW <- b
					// IF Status != 0 - report bind failure state
					if r.Status != 0 {
						time.Sleep(100 * time.Millisecond)
						s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPBindFailed}, fmt.Errorf("Bind validator returned error code [%d]", r.Status), nil)
						return
					}
				case <-s.Closed:
					return
				}
			} else {
				// Automatic acknowledge bind request
				b := s.EncodeBindRespRAW(p.Hdr.ID, p.Hdr.Seq, 0, "GO-SMPP-AUTO")
				s.OutboxRAW <- b
			}

			// Report bound state
			s.reportStateS(connState{ts: CTCPIncoming, ss: CSMPPBound}, nil, nil)

			// Start tracking packet expiration
			s.trackPacketTimeout()

			// Start ENQUIRE_LINK Generator
			go s.enquireSender(10)

		// =============================================================
		// BIND_RECEIVER_RESP/BIND_TRANSMITTER_RESP/BIND_TRANSCIEVER_RESP
		case libsmpp.CMD_BIND_RECEIVER_RESP, libsmpp.CMD_BIND_TRANSMITTER_RESP, libsmpp.CMD_BIND_TRANSCIEVER_RESP:
			if s.Cs.ss != CSMPPBindSent {
				// Invalid bind state
				s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Received BIND_RESP packet, while session is in %d (%s) state", s.Cs.GetSMPPState(), s.Cs.GetSMPPState().String()), nil)
				return
			}
			// Handle BIND request
			if st, sid, erx := s.DecodeBindResp(p); erx == nil {
				// Success
				if st == 0 {
					// Report Bound state
					s.reportStateS(connState{ss: CSMPPBound}, nil, nil)
					s.Bind.SMSCID = sid

					// Start ENQUIRE_LINK Generator
					go s.enquireSender(10)
				} else {
					s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Received BIND_RESP packet with error code %d", st), nil)
					return
				}
			} else {
				// Received ERROR from DecodeBindResp
				s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("DecodeBindResp() returned an error"), erx)
			}

		// =============================================================
		// ENQUIRE_LINK
		case libsmpp.CMD_ENQUIRE_LINK:
			s.enquireResponder(p, s.allocateSeqNo())

		// =============================================================
		// UNBIND
		case libsmpp.CMD_UNBIND:
			// Unbind requst, drop connection
			if s.DebugLevel > 1 {
				fmt.Println("Incoming UNBIND request")
			}
			return

		// =============================================================
		// SUBMIT_SM/DELIVER_SM/QUERY/REPLACE/CANCEL
		case libsmpp.CMD_SUBMIT_SM, libsmpp.CMD_DELIVER_SM, libsmpp.CMD_QUERY_SM, libsmpp.CMD_REPLACE_SM, libsmpp.CMD_CANCEL_SM:
			// Put SUBMIT_SM, DELIVER_SM packet into Inbox
			px := &SMPPPacket{
				Hdr:        SMPPHeader{ID: p.Hdr.ID, Len: p.Hdr.ID, Seq: p.Hdr.Seq, Status: p.Hdr.Status},
				BodyLen:    p.Hdr.Len - 16,
				CreateTime: time.Now(),
			}
			if p.Hdr.Len > 16 {
				px.Body = make([]byte, p.Hdr.Len-16)
				copy(px.Body, p.Body)
			}
			s.winTrackEvent(CDirIncoming, p)

			s.Inbox <- *px

		// =============================================================
		// SUBMIT_SM_RESP/DELIVER_SM_RESP
		case libsmpp.CMD_SUBMIT_SM_RESP, libsmpp.CMD_DELIVER_SM_RESP, libsmpp.CMD_QUERY_SM_RESP, libsmpp.CMD_REPLACE_SM_RESP, libsmpp.CMD_CANCEL_SM_RESP:
			px := &SMPPPacket{
				Hdr:     SMPPHeader{ID: p.Hdr.ID, Len: p.Hdr.ID, Seq: p.Hdr.Seq, Status: p.Hdr.Status},
				BodyLen: p.Hdr.Len - 16,
				IsReply: true,
			}
			if p.Hdr.Len > 16 {
				px.Body = make([]byte, p.Hdr.Len-16)
				copy(px.Body, p.Body)
			}

			// Process message and mark if this is duplicated response
			px.IsDuplicate = s.winTrackEvent(CDirIncoming, p)
			px.UplinkTransactionID = p.UplinkTransactionID

			s.InboxR <- *px

		default:
		}
	}
}
