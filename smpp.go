package libsmpp

import (
	"encoding/binary"
	"fmt"
	log "github.com/sirupsen/logrus"
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

	// Incoming messages (SUBMIT_SM/DELIVER_SM) from SMPP link
	s.Inbox = make(chan SMPPPacket)
	// Incoming replies (SUBMIT_SM_RESP/DELIVER_SM_RESP) from SMPP link
	s.InboxR = make(chan SMPPPacket)

	// Outgoing messages and replies to SMPP link
	s.Outbox = make(chan SMPPPacket)

	// Outgoing RAW messages to SMPP link (only BIND_RESP and ENQUIRE_LINK/ENQUIRE_LINK_RESP packets)
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

	if s.TimeoutSubmitErrorCode == 0 {
		s.TimeoutSubmitErrorCode = libsmpp.SMPP_TIMEOUT_SUBMIT_ERROR_CODE
	}

	if s.TimeoutDeliverErrorCode == 0 {
		s.TimeoutDeliverErrorCode = libsmpp.SMPP_TIMEOUT_DELIVER_ERROR_CODE
	}

	// Allocate map for packet tracking
	s.TrackRX = make(map[uint32]SMPPTracking, 1000)
	s.TrackTX = make(map[uint32]SMPPTracking, 1000)
}

func (s *SMPPSession) GetTrackQueueSize() (tx int, rx int) {
	s.winMutex.RLock()
	tx = len(s.TrackTX)
	rx = len(s.TrackRX)
	s.winMutex.RUnlock()
	return tx, rx
}

func (s *SMPPSession) Close(origin string) {
	var f bool
	s.closeMTX.Lock()
	select {
	case <-s.Closed:
	default:
		f = true
		close(s.Closed)
	}
	s.closeMTX.Unlock()
	if f {
		log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Close"}).Info("Close is called from [ ", origin, " ]")
	}
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
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "bindTimeouter", "timeout": t}).Info("Timeout raised, closing connection")
			s.conn.Close()
		}
	case <-s.Closed:
		return
	}
}

// Packet expiration tracker
func (s *SMPPSession) trackPacketTimeout() {
	tk := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tk.C:
			// TODO
			// 1. Search for expired packets
			s.winMutex.RLock()
			// fmt.Println("trackPacketTimeout TICK [", len(s.TrackRX), ", ", len(s.TrackTX), "]")
			for k, v := range s.TrackRX {
				if time.Since(v.T) > time.Duration(s.RXMaxTimeoutMS)*time.Millisecond {
					log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "TimeoutTracker", "action": "RX", "packet": k}).Warning("RX Timeout")

					// Generate reply packet
					switch v.CommandID {
					case libsmpp.CMD_DELIVER_SM:
						p := s.EncodeDeliverSmResp(SMPPPacket{Hdr: SMPPHeader{ID: v.CommandID, Seq: v.SeqNo}, SeqComplete: true}, s.TimeoutDeliverErrorCode)
						p.IsUntrackable = true
						log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "TimeoutTracker", "action": "RX", "packet": k}).Info("Send DELIVER_SM_RESP")
						s.Outbox <- p
					case libsmpp.CMD_SUBMIT_SM:
						p := s.EncodeSubmitSmResp(SMPPPacket{Hdr: SMPPHeader{ID: v.CommandID, Seq: v.SeqNo}, SeqComplete: true}, s.TimeoutSubmitErrorCode, "")
						p.IsUntrackable = true
						log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "TimeoutTracker", "action": "RX", "packet": k}).Info("Send SUBMIT_SM_RESP")
						s.Outbox <- p
					default:
						log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "TimeoutTracker", "action": "RX", "packet": k}).Info("RX Timeout - unsupported CommandID: ", v.CommandID)
					}
					// Delete record from tracking
					delete(s.TrackRX, k)
				}
			}
			for k, v := range s.TrackTX {
				if time.Since(v.T) > time.Duration(s.TXMaxTimeoutMS)*time.Millisecond {
					fmt.Println("#", k, " - Expired TX packet (TXlen=", len(s.TrackTX), ")")

					// TODO - Implement TX timeout behaviour, have to send Timeouted notification to InboxR

					// Delete record from tracking
					delete(s.TrackTX, k)
				}
			}
			s.winMutex.RUnlock()

		case <-s.Closed:
			return
		}
	}
}

// Allocate SEQUENCE number
func (s *SMPPSession) allocateSeqNo() uint32 {
	s.seqMTX.Lock()
	s.LastTXSeq++
	x := s.LastTXSeq
	s.seqMTX.Unlock()
	return x
}

func (s *SMPPSession) enquireSender(t int) error {
	log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "EnquireSender", "tick": t}).Info("Starting ENQUIRE SENDER")
	tk := time.NewTicker(time.Duration(t) * time.Second)
	for {
		select {
		case <-tk.C:
			// Initiate sending of outgoing ENQUIRE_LINK packet
			seq := s.allocateSeqNo()
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "EnquireSender", "seq": seq}).Debug("Sent")

			s.Enquire.Lock()
			s.Enquire.Sent.ID = seq
			s.Enquire.Sent.Date = time.Now()
			s.Enquire.Unlock()

			s.OutboxRAW <- s.EncodeEnquireLinkRAW(seq)
		case <-s.Closed:
			return nil
		}
	}
	return nil
}

func (s *SMPPSession) enquireResponder(p *SMPPPacket) {
	s.OutboxRAW <- s.EncodeEnquireLinkRespRAW(p.Hdr.Seq)
}

// Packet REQ/RESP tracking engine
// dir ConnDirection:
// * CDirOutgoing - packets, that are sent to socket
// * CDirIncoming - packets. that are received from socket
// flagDropPacket - FLAG if packet shouldn't be delivered to recipient, because it is already delivered by timeout or this is wrong packet
func (s *SMPPSession) winTrackEvent(dir ConnDirection, p *SMPPPacket) (flagDropPacket bool) {
	// Lock mutex
	s.winMutex.Lock()

	// Process CommandID
	switch p.Hdr.ID {
	case libsmpp.CMD_SUBMIT_SM, libsmpp.CMD_DELIVER_SM, libsmpp.CMD_QUERY_SM, libsmpp.CMD_REPLACE_SM, libsmpp.CMD_CANCEL_SM:
		if dir == CDirIncoming {
			s.RXWindow++

			// Register new tracking message
			s.TrackRX[p.Hdr.Seq] = SMPPTracking{
				SeqNo:      p.Hdr.Seq,
				CommandID:  p.Hdr.ID,
				T:          time.Now(),
				CreateTime: p.CreateTime,
			}
		}
		if dir == CDirOutgoing {
			s.TXWindow++

			// Register new tracking message
			s.TrackTX[p.Hdr.Seq] = SMPPTracking{
				SeqNo:               p.Hdr.Seq,
				CommandID:           p.Hdr.ID,
				T:                   time.Now(),
				CreateTime:          p.CreateTime,
				UplinkTransactionID: p.UplinkTransactionID,
			}
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "WinTrackTX", "action": "set", "seq": p.Hdr.Seq, "UpTransID": p.UplinkTransactionID}).Debug("Save Data")
		}
	case libsmpp.CMD_SUBMIT_SM_RESP, libsmpp.CMD_DELIVER_SM_RESP, libsmpp.CMD_QUERY_SM_RESP, libsmpp.CMD_REPLACE_SM_RESP, libsmpp.CMD_CANCEL_SM_RESP:
		if dir == CDirIncoming {
			s.TXWindow--
			if x, ok := s.TrackTX[p.Hdr.Seq]; ok {
				// Preserve UplingTransactionID for reply packet
				p.UplinkTransactionID = x.UplinkTransactionID
				p.OrigTime = x.CreateTime
				p.NetOrigTime = x.T
				log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "WinTrackTX", "action": "get", "seq": p.Hdr.Seq, "UpTransID": x.UplinkTransactionID}).Debug("Get Data")

				// Remove tracking of sent packet
				delete(s.TrackTX, p.Hdr.Seq)
			} else {
				if !p.IsUntrackable {
					// Received unhandled _RESP packet
					log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "WinTrackTX", "action": "get", "state": "lost", "seq": p.Hdr.Seq}).Debug("Drop untracked _RESP packet")

					// Mark, that packet will be duplicated
					flagDropPacket = true
				}
			}
		}
		if dir == CDirOutgoing {
			s.RXWindow--
			if _, ok := s.TrackRX[p.Hdr.Seq]; ok {
				// TODO - Track message processing rime

				// Remove tracking of received packet
				delete(s.TrackRX, p.Hdr.Seq)
			} else {
				if !p.IsUntrackable {
					// Received unhandled _RESP packet
					log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "WinTrackRX", "action": "get", "seq": p.Hdr.Seq}).Debug("Drop untracked _RESP packet")

					// Mark, that packet will be duplicated
					flagDropPacket = true
				}
			}
		}
	}
	s.winMutex.Unlock()

	return
}

// Take messages from outbox and send messages to the wire
func (s *SMPPSession) processOutbox() {
	defer s.Close("processOutbox")

	log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Outbox"}).Info("Starting OUTBOX Processor")
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
				log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Outbox", "action": "write", "count": n}).Info("Outbox: error writing to socket", err)
				return
			}
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Outbox", "action": "write", "count": n}).Trace("Outbox: sent to socket")

		case p := <-s.OutboxRAW:
			// Send RAW packet from SMPPPacket.Body
			n, err := s.conn.Write(p)
			if err != nil {
				// BREAK CONNECTION
				log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Outbox", "action": "write", "count": n}).Info("OutboxRAW: error writing to socket", err)
				return
			}
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Outbox", "action": "write", "count": n}).Trace("OutboxRAW: sent to socket")

		case <-s.Closed:
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Outbox", "action": "close"}).Info("Closing Outbox processor")
			return
		}
	}

}

//
func (s *SMPPSession) RunOutgoing(conn *net.TCPConn, b SMPPBind, id uint32) {
	s.Run(conn, CDirOutgoing, b, id)
}

func (s *SMPPSession) RunIncoming(conn *net.TCPConn, id uint32) {
	s.Run(conn, CDirIncoming, SMPPBind{}, id)
}

//
func (s *SMPPSession) Run(conn *net.TCPConn, cd ConnDirection, cb SMPPBind, id uint32) {
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
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Run", "action": "ReadFull"}).Debug("Error reading packet header (16 bytes)!", err)
			s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Error reading incoming packet header (16 bytes)!"), err)
			return
		}
		// Decode header
		if err := p.DecodeHDR(hdrBuf); err != nil {
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Run", "action": "DecodeHDR"}).Debug("Error decoding packet", err)
			s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Error decoding header!"), err)
			return
		}

		// Validate SMPP packet size
		if p.Hdr.Len > MaxSMPPPacketSize {
			// Invalid packet. Break
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Run", "action": "ValidateHeader"}).Debug("Incoming packet is too large!")
			s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Incoming packet is too large (%d, while MaxPacketSize is %d)", p.Hdr.Len, MaxSMPPPacketSize), nil)
			return
		}

		// Read least part of the packet
		if p.Hdr.Len > 16 {
			if _, err := io.ReadFull(conn, buf[0:p.Hdr.Len-16]); err != nil {
				log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "Run", "action": "ReadFull"}).Debug("Error reading last part of the packet!", err)
				s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPClosed}, fmt.Errorf("Error reading last part of the packet!"), err)
				return
			}
		}
		log.WithFields(log.Fields{"type": "smpp", "service": "PacketLoop", "SID": s.SessionID, "action": fmt.Sprintf("%x (%s)", p.Hdr.ID, CmdName(p.Hdr.ID)), "Seq": p.Hdr.Seq, "Len": p.Hdr.Len}).Trace(fmt.Sprintf("%x", buf[0:p.Hdr.Len-16]))

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
				log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "PacketLoop", "action": "BIND", "SystemID": s.Bind.SystemID}).Info("Received")

				if s.DebugLevel > 1 {
					fmt.Println("[", s.SessionID, "] Incoming BIND request")
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
						log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "PacketLoop", "action": "BIND", "SystemID": s.Bind.SystemID}).Info("Rejected")
						time.Sleep(100 * time.Millisecond)
						s.reportStateS(connState{ts: CTCPClosed, ss: CSMPPBindFailed}, fmt.Errorf("Bind validator returned error code [%d]", r.Status), nil)
						return
					}
					log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "PacketLoop", "action": "BIND", "SystemID": s.Bind.SystemID}).Info("Accepted")
				case <-s.Closed:
					return
				}
			} else {
				// Automatic acknowledge bind request
				b := s.EncodeBindRespRAW(p.Hdr.ID, p.Hdr.Seq, 0, "GO-SMPP-AUTO")
				s.OutboxRAW <- b
				log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "PacketLoop", "action": "BIND", "SystemID": s.Bind.SystemID}).Info("AUTO-Accepted")
			}

			// Report bound state
			s.reportStateS(connState{ts: CTCPIncoming, ss: CSMPPBound}, nil, nil)

			// Start tracking packet expiration
			go s.trackPacketTimeout()

			// Start ENQUIRE_LINK Generator
			go s.enquireSender(60)

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

					// Start tracking packet expiration
					go s.trackPacketTimeout()

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
			s.Enquire.Lock()
			s.Enquire.Recv.ID = p.Hdr.Seq
			s.Enquire.Recv.Date = time.Now()
			s.Enquire.Unlock()

			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "PacketLoop", "action": "ENQUIRE_LINK", "seq": p.Hdr.Seq}).Debug("Received")

			s.enquireResponder(p)

		// =============================================================
		// UNBIND
		case libsmpp.CMD_UNBIND:
			// Unbind requst, drop connection
			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "PacketLoop", "action": "UNBIND", "seq": p.Hdr.Seq}).Debug("Received")
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
			s.winTrackEvent(CDirIncoming, px)

			s.Inbox <- *px

		// =============================================================
		// SUBMIT_SM_RESP/DELIVER_SM_RESP
		case libsmpp.CMD_SUBMIT_SM_RESP, libsmpp.CMD_DELIVER_SM_RESP, libsmpp.CMD_QUERY_SM_RESP, libsmpp.CMD_REPLACE_SM_RESP, libsmpp.CMD_CANCEL_SM_RESP:
			px := &SMPPPacket{
				Hdr:        SMPPHeader{ID: p.Hdr.ID, Len: p.Hdr.ID, Seq: p.Hdr.Seq, Status: p.Hdr.Status},
				BodyLen:    p.Hdr.Len - 16,
				CreateTime: time.Now(),
				IsReply:    true,
			}
			if p.Hdr.Len > 16 {
				px.Body = make([]byte, p.Hdr.Len-16)
				copy(px.Body, p.Body)
			}

			// Process message and mark if this is duplicated response
			px.IsDuplicate = s.winTrackEvent(CDirIncoming, px)
			px.UplinkTransactionID = p.UplinkTransactionID

			s.InboxR <- *px

		// =============================================================
		// ENQUIRE_LINK_RESP
		case libsmpp.CMD_ENQUIRE_LINK_RESP:
			seq := p.Hdr.Seq
			s.Enquire.Lock()
			s.Enquire.Sent.Ack.ID = seq
			s.Enquire.Sent.Ack.Date = time.Now()
			s.Enquire.Unlock()

			log.WithFields(log.Fields{"type": "smpp", "SID": s.SessionID, "service": "EnquireSender", "seq": seq}).Debug("Confirmed")

		default:
		}
	}
}
