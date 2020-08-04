package libsmpp

import (
	"sync"
	"time"
)

// PoolQueue SessionID
type PQSessionID uint32

// Session Pool Entry
type SPEntry struct {
	SessionID PQSessionID  // ID of the session
	Session   *SMPPSession // Reference to Session

	IsClosed bool // FLAG: Is connection closed
}

// Structure for routed packet tracking (reply routing)
type PoolPacketTracking struct {
	OrigSessionID PQSessionID // Originator sessionID
	DestSessionID PQSessionID // Recipient sessionID
	T             time.Time   // Routing origination time
	TransactionID uint32      // UNIQ Transaction ID [KEY] for routing
	Packet        SMPPPacket  // Original SMPP packet
}

// Session Pool
type SessionPool struct {
	Pool      map[PQSessionID]*SPEntry // Pool for storing sessions
	poolMutex sync.RWMutex             //

	Queue chan PQPacket // Outgoing queue from all SMPP Sessions
	QResp chan PQPacket // RESPONSE Queue for packet delivery

	maxSessionID PQSessionID
	sessionMutex sync.RWMutex

	maxTransaction uint32
	//	transactionMutex sync.RWMutex

	Track      map[uint32]PoolPacketTracking
	trackMutex sync.RWMutex

	RoutePolicy []PRoutePolicy
	//	routeMutex  sync.RWMutex
}

// Pool Queue Packet
type PQPacket struct {
	OrigSessionID PQSessionID // Originator sessionID
	DestSessionID PQSessionID // Recipient sessionID
	T             time.Time   // Packet origination timestamp

	IsReply   bool   // FLAG: This is reply
	PacketSeq uint32 // SeqNo of the original/reply packet

	Packet SMPPPacket // SMPP Packet
}

// Routing policy entry
type PRoutePolicy struct {
	origID string
	destID string
}

func (p *SessionPool) GetSystemIdBySessionID(id PQSessionID) (string, bool) {
	p.sessionMutex.RLock()
	defer p.sessionMutex.RUnlock()

	if o, ok := p.Pool[id]; ok {
		return o.Session.Bind.SystemID, ok
	}
	return "", false
}

func (p *SessionPool) GetSessionIdBySystemID(id string) (PQSessionID, bool) {
	p.sessionMutex.RLock()
	defer p.sessionMutex.RUnlock()
	for k, v := range p.Pool {
		if !v.IsClosed && (v.Session.Bind.SystemID == id) {
			return k, true
		}
	}
	return 0, false
}

type SessionListInfo struct {
	Id     PQSessionID
	Closed bool
	Cs     connState
	Bind   SMPPBind
	MaxSeq uint32
}

func (p *SessionPool) GetSessionList() (l []SessionListInfo) {
	p.sessionMutex.RLock()
	defer p.sessionMutex.RUnlock()
	for k, v := range p.Pool {
		v.Session.seqMTX.RLock()
		l = append(l, SessionListInfo{
			Id:     k,
			Closed: v.IsClosed,
			Cs:     v.Session.Cs,
			Bind:   v.Session.Bind,
			MaxSeq: v.Session.LastTXSeq,
		})
		v.Session.seqMTX.RUnlock()
	}
	return
}
