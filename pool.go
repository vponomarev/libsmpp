package libsmpp

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"libsmpp/const"
	//	"net"
	// "sync"
	"time"
)

func (p *SessionPool) Init() {
	p.Pool = make(map[PQSessionID]*SPEntry)
	p.Queue = make(chan PQPacket)
	p.QResp = make(chan PQPacket)

	// Init map for packet tracking
	p.Track = make(map[uint32]PoolPacketTracking, 512)

	// Sample routing policy
	p.RoutePolicy = []PRoutePolicy{}
	p.RoutePolicy = append(p.RoutePolicy, PRoutePolicy{"test_antispam", "test_dp"})
	p.RoutePolicy = append(p.RoutePolicy, PRoutePolicy{"test_dp", "test_antispam"})

	// Start Queue Resp Manager
	go p.QDeliveryManager()

	// Start Queue Manager
	go p.QManager()
}

func (p *SessionPool) allocateTransactionID() (r uint32) {
	p.sessionMutex.RLock()
	p.maxTransaction++
	r = p.maxTransaction
	p.sessionMutex.RUnlock()
	return
}

// Register SMPPSession in pool
func (p *SessionPool) RegisterSession(s *SMPPSession) {
	p.sessionMutex.RLock()
	p.maxSessionID++

	pe := &SPEntry{
		SessionID: p.maxSessionID,
		Session:   s,
	}
	log.WithFields(log.Fields{"type": "pool", "SID": pe.SessionID, "service": "RegisterSession"}).Info("New session registered")

	// Register PoolEntry in slice
	p.Pool[pe.SessionID] = pe
	p.sessionMutex.RUnlock()

	// Start session traffic processing
	for {
		select {
		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "pool", "SID": pe.SessionID, "service": "RegisterSession", "action": "StatusUpdate"}).Info(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())

		case x := <-s.Inbox:
			// Prepare packet for sending to Pool inbox
			pp := PQPacket{
				OrigSessionID: pe.SessionID,
				DestSessionID: 0,
				T:             time.Now(),
				IsReply:       false,
				PacketSeq:     0,
				Packet:        x,
			}
			p.Queue <- pp

		//
		case x := <-s.InboxR:
			// Try to unpark message
			log.WithFields(log.Fields{"type": "pool", "SID": pe.SessionID, "service": "RegisterSession", "action": "Inbox", "UTID": x.UplinkTransactionID}).Info("Incoming QuickReply: ", x)

			// Check if we have parked record
			if x.UplinkTransactionID > 0 {
				p.trackMutex.RLock()
				tr, ok := p.Track[x.UplinkTransactionID]
				p.trackMutex.RUnlock()

				if ok {
					// Prepare record for sending to originator via QResp
					x.Hdr.Seq = tr.Packet.Hdr.Seq
					x.SeqComplete = true
					pp := PQPacket{
						OrigSessionID: pe.SessionID,
						DestSessionID: tr.OrigSessionID,
						T:             time.Now(),
						IsReply:       true,
						PacketSeq:     0,
						Packet:        x,
					}

					// Delete message from parking map
					p.trackMutex.RLock()
					delete(p.Track, pp.Packet.UplinkTransactionID)
					p.trackMutex.RUnlock()

					p.QResp <- pp
				} else {
					// - identity lost
					fmt.Println("[", pe.SessionID, "] QuickPath REPLY: Received untracked packet: ", x)
				}
			} else {
				// No UplinkTransactionID is set
				fmt.Println("[", pe.SessionID, "] QuickPath REPLY: Reply does not contain UplinkTransactionID: ", x)
			}

		case <-s.Closed:
			fmt.Println("[", pe.SessionID, "] SP.RegisterSession: Session closed")

			// Remove session from pool
			p.sessionMutex.RLock()
			delete(p.Pool, pe.SessionID)
			p.sessionMutex.RUnlock()

			// Return
			return
		}
	}
}

// Manage message queue
func (p *SessionPool) QManager() {
	var msgID uint32
	for {
		select {
		case pp := <-p.Queue:
			msgID++
			origSession := pp.OrigSessionID

			fmt.Println("[", origSession, "=>", pp.DestSessionID, "] SP.QManager# [", msgID, "] Incoming packet: ", pp.Packet)

			// ==========================================================
			// HERE IS ROUTING ENGINE
			// ==========================================================

			// We're implementing Load Balancer
			// This mean: each pool contain only INCOMING session + group of OUTGOING sessions.
			// Routing will be done between INCOMING <=> OUTGOUING sessions

			p.poolMutex.RLock()
			sDir := p.Pool[origSession].Session.Cs.GetDirection()

			destSession := pp.DestSessionID
			for k, v := range p.Pool {
				if (k != origSession) && (v.Session.Cs.GetDirection() != sDir) {
					destSession = k
					break
				}
			}
			p.poolMutex.RUnlock()

			// IF destSession is specified - do route packet
			if destSession > 0 {

				// Route packet
				pn := pp
				pn.DestSessionID = destSession
				pn.Packet.UplinkTransactionID = p.allocateTransactionID()

				// Save tracking information
				p.trackMutex.RLock()
				p.Track[pn.Packet.UplinkTransactionID] = PoolPacketTracking{
					OrigSessionID: pn.OrigSessionID,
					DestSessionID: pn.DestSessionID,
					T:             time.Now(),
					TransactionID: pn.Packet.UplinkTransactionID,
					Packet:        pn.Packet,
				}
				p.trackMutex.RUnlock()

				fmt.Println("[", pp.OrigSessionID, "] Message is routed to: ", destSession)
				p.QResp <- pn
			} else {
				// Route undefined
				fmt.Println("[", pp.OrigSessionID, "] Route is not found, generate self-response")
				// Generate response [ SELF-RESPONSE ]
				sx := SMPPSession{}

				pn := pp
				pn.DestSessionID = origSession
				pn.OrigSessionID = 0
				pn.IsReply = true
				switch pp.Packet.Hdr.ID {
				case libsmpp.CMD_SUBMIT_SM:
					pn.Packet = sx.EncodeSubmitSmResp(pp.Packet, 0, fmt.Sprintf("QDR-MSG-%d", msgID))
				case libsmpp.CMD_DELIVER_SM:
					pn.Packet = sx.EncodeDeliverSmResp(pp.Packet, 0)
				default:
					pn.Packet = sx.EncodeGenericNack(pp.Packet, 0)
				}
				p.QResp <- pn
			}

		}
	}
}

// Manage QueueResp packets
func (p *SessionPool) QDeliveryManager() {
	for {
		select {
		case pp := <-p.QResp:
			fmt.Println("[", pp.OrigSessionID, "=>", pp.DestSessionID, "][UTID:", pp.Packet.UplinkTransactionID, "] SP.QDeliveryManager#  packet: ", pp.Packet)

			destSession := pp.DestSessionID

			// Search for a session with specified SessionID
			var ps *SMPPSession
			p.poolMutex.RLock()
			if px, ok := p.Pool[destSession]; ok && !px.IsClosed {
				ps = px.Session
			}
			p.poolMutex.RUnlock()

			// Send packet to Outbox of specified Session
			if ps != nil {
				ps.Outbox <- pp.Packet
			} else {
				fmt.Println("[", pp.OrigSessionID, "] SP.QDeliveryManager# CANNOT FIND DESTINATION SESSION")
			}
		}
	}
}
