package libsmpp

import (
	"fmt"
	"libsmpp/const"
	"net"
	"sync"
	"time"
)

const MaxSMPPPacketSize = 8192

//
//
type ConnDirection uint8
type ConnTCPState uint8
type ConnSMPPState uint8
type ConnSMPPMode uint8

type SMPPHeader struct {
	Len    uint32
	ID     uint32
	Status uint32
	Seq    uint32
}

func (h SMPPHeader) String() string {
	return fmt.Sprintf("[%d|%d,%s|%d|%d bytes]", h.Seq, h.ID, CMDNameMapping[h.ID], h.Status, h.Len)
}

type SMPPPacket struct {
	Hdr  SMPPHeader
	Body []byte

	BodyLen     uint32 // Lenght of the data body
	SeqComplete bool   // FLAG: SequenceNumber is complete
	IsReply     bool   // FLAG: This is reply
	IsDuplicate bool   // FLAG: If this is duplicate confirmation

	CreateTime          time.Time // Packet origination timestamp
	UplinkTransactionID uint32
}

type ConnState interface {
	GetDirection() ConnDirection
	GetTCPState() ConnTCPState
	GetSMPPState() ConnSMPPState
	GetSMPPMode() ConnSMPPMode
	Error() error
	NError() error

	SetDirection(cd ConnDirection)
}

type connState struct {
	cd   ConnDirection
	ts   ConnTCPState
	ss   ConnSMPPState
	sm   ConnSMPPMode
	err  error // Error
	nerr error // Nested error
}

func (c *connState) GetDirection() ConnDirection { return c.cd }
func (c *connState) GetTCPState() ConnTCPState   { return c.ts }
func (c *connState) GetSMPPState() ConnSMPPState { return c.ss }
func (c *connState) GetSMPPMode() ConnSMPPMode   { return c.sm }

func (c *connState) Error() error  { return c.err }
func (c *connState) NError() error { return c.nerr }

func (c *connState) SetDirection(cd ConnDirection) { c.cd = cd }

// Connection mode: Undefixed, Incoming, Outgoing
const (
	CDirUndefined ConnDirection = iota
	CDirIncoming
	CDirOutgoing
)

var connDirectionText = map[ConnDirection]string{
	CDirUndefined: "Unknown",
	CDirIncoming:  "Incoming",
	CDirOutgoing:  "Outgoing",
}

func (x ConnDirection) String() string { return connDirectionText[x] }

// TCP Session state: Undefixed, Incoming, Outgoing
const (
	CTCPUndefined ConnTCPState = iota
	CTCPIncoming
	CTCPOutgoing
	CTCPClosed
)

var connTCPStateText = map[ConnTCPState]string{
	CTCPUndefined: "Unknown",
	CTCPIncoming:  "Incoming",
	CTCPOutgoing:  "Outgoing",
	CTCPClosed:    "Closed",
}

func (x ConnTCPState) String() string { return connTCPStateText[x] }

// SMPP session state: Idle, BindReceived, BindSent, BindFailed, Bound
const (
	CSMPPIdle ConnSMPPState = iota
	CSMPPWaitForBind
	CSMPPBindReceived
	CSMPPBindSent
	CSMPPBindFailed
	CSMPPBound
	CSMPPClosed
)

var connSMPPStateText = map[ConnSMPPState]string{
	CSMPPIdle:         "Idle",
	CSMPPWaitForBind:  "WaitForBind",
	CSMPPBindReceived: "BindReceived",
	CSMPPBindSent:     "BindSent",
	CSMPPBindFailed:   "BindFailed",
	CSMPPBound:        "Bound",
	CSMPPClosed:       "Closed",
}

func (x ConnSMPPState) String() string { return connSMPPStateText[x] }

// SMPP Bind mode: TX, RX, TRX
const (
	CSMPPUndefined ConnSMPPMode = iota
	CSMPPTX
	CSMPPRX
	CSMPPTRX
)

var connSMPPModeText = map[ConnSMPPMode]string{
	CSMPPUndefined: "Undefied",
	CSMPPTX:        "TX",
	CSMPPRX:        "RX",
	CSMPPTRX:       "TRX",
}

func (x ConnSMPPMode) String() string { return connSMPPModeText[x] }

type SMPPBind struct {
	ConnMode   ConnSMPPMode
	SystemID   string
	Password   string
	SystemType string
	IVersion   uint
	AddrTON    uint
	AddrNPI    uint
	AddrRange  string
	SMSCID     string
}

type BindValidatorRequest struct {
	Bind SMPPBind
	ID   uint32
}
type BindValidatorResponce struct {
	ID     uint32
	SMSCID string
	Status uint32
}

// SMPP Session
type SMPPSession struct {
	SessionID uint32 // Uniq sessionID, used for logging

	Cs     connState
	Bind   SMPPBind
	Status chan ConnState
	Closed chan interface{}
	conn   *net.TCPConn

	LastTXSeq uint32
	seqMTX    sync.RWMutex

	// Enquire Link related information
	Enquire struct {
		Sent struct {
			ID   uint32
			Date time.Time
			Ack  struct {
				ID   uint32
				Date time.Time
			}
		}
		Recv struct {
			ID   uint32
			Date time.Time
		}
		sync.RWMutex
	}

	TXMaxTimeoutMS uint32 // Maximum time we wait for reply from external platform [ send error confirmation in case of trigger ]
	RXMaxTimeoutMS uint32 // Max timeout we wait for reply from our buddy (in case of manual confirmation) [ send confirmation to peer in case of trigger ]

	RXWindow uint32
	TXWindow uint32
	winMutex sync.RWMutex

	// Channel for INCOMING packets (SUBMIT_SM/DELIVER_SM/CANCEL_SM/REPLACE_SM/QUERY_SM)
	Inbox chan SMPPPacket

	// Channel for INCOMING ACK packets (_RESP)
	InboxR chan SMPPPacket

	// Send outgoing packets
	Outbox    chan SMPPPacket
	OutboxRAW chan []byte

	// SMPP Bind validator
	BindValidator  chan BindValidatorRequest
	BindValidatorR chan BindValidatorResponce

	//
	ManualBindValidate bool

	DebugLevel uint

	// Tracking RESP for TX (sent out) and RX (received) packets
	TrackTX map[uint32]SMPPTracking
	TrackRX map[uint32]SMPPTracking
}

type SMPPTracking struct {
	SeqNo     uint32    // SMPP Session SeqNo
	CommandID uint32    // Original SMPP CommandID
	T         time.Time // Packet origination time

	UplinkTransactionID uint32 // Uniq packet ID, provided by UPLINK
}

var CMDNameMapping = map[uint32]string{
	libsmpp.CMD_GENERIC_NACK:          "GENERIC_NACK",
	libsmpp.CMD_BIND_RECEIVER:         "BIND_RECEIVER",
	libsmpp.CMD_BIND_RECEIVER_RESP:    "BIND_RECEIVER_RESP",
	libsmpp.CMD_BIND_TRANSMITTER:      "BIND_TRANSMITTER",
	libsmpp.CMD_BIND_TRANSMITTER_RESP: "BIND_TRANSMITTER_RESP",
	libsmpp.CMD_QUERY_SM:              "QUERY_SM",
	libsmpp.CMD_QUERY_SM_RESP:         "QUERY_SM_RESP",
	libsmpp.CMD_SUBMIT_SM:             "SUBMIT_SM",
	libsmpp.CMD_SUBMIT_SM_RESP:        "SUBMIT_SM_RESP",
	libsmpp.CMD_DELIVER_SM:            "DELIVER_SM",
	libsmpp.CMD_DELIVER_SM_RESP:       "DELIVER_SM_RESP",
	libsmpp.CMD_UNBIND:                "UNBIND",
	libsmpp.CMD_UNBIND_RESP:           "UNBIND_RESP",
	libsmpp.CMD_REPLACE_SM:            "REPLACE_SM",
	libsmpp.CMD_REPLACE_SM_RESP:       "REPLACE_SM_RESP",
	libsmpp.CMD_CANCEL_SM:             "CANCEL_SM",
	libsmpp.CMD_CANCEL_SM_RESP:        "CANCEL_SM_RESP",
	libsmpp.CMD_BIND_TRANSCIEVER:      "BIND_TRANSCIEVER",
	libsmpp.CMD_BIND_TRANSCIEVER_RESP: "BIND_TRANSCIEVER_RESP",
	libsmpp.CMD_OUTBIND:               "OUTBIND",
	libsmpp.CMD_ENQUIRE_LINK:          "ENQUIRE_LINK",
	libsmpp.CMD_ENQUIRE_LINK_RESP:     "ENQUIRE_LINK_RESP",
	libsmpp.CMD_SUBMIT_MULTI:          "SUBMIT_MULTI",
	libsmpp.CMD_SUBMIT_MULTI_RESP:     "SUBMIT_MULTI_RESP",
	libsmpp.CMD_ALERT_NOTIFICATION:    "ALERT_NOTIFICATION",
	libsmpp.CMD_DATA_SM:               "DATA_SM",
	libsmpp.CMD_DATA_SM_RESP:          "DATA_SM_RESP",
}

func CmdName(id uint32) string {
	return CMDNameMapping[id]
}
