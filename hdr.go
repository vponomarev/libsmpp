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

	BodyLen       uint32 // Lenght of the data body
	SeqComplete   bool   // FLAG: SequenceNumber is complete
	IsReply       bool   // FLAG: This is reply
	IsDuplicate   bool   // FLAG: If this is duplicate confirmation
	IsUntrackable bool   // FLAG: If this packet shouldn't be dropped if it is not tracked

	CreateTime  time.Time // Packet origination timestamp
	NetSentTime time.Time // Time, when packet was sent to network

	// Reply packet extra info
	OrigTime            time.Time // Time, when original packet was generated
	NetOrigTime         time.Time // Time, when original packet was sent to network
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

	Cs       connState
	Bind     SMPPBind
	Status   chan ConnState
	Closed   chan interface{}
	closeMTX sync.RWMutex

	conn *net.TCPConn

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

	TXMaxTimeoutMS          uint32 // Maximum time we wait for reply from external platform [ send error confirmation in case of trigger ]
	RXMaxTimeoutMS          uint32 // Max timeout we wait for reply from our buddy (in case of manual confirmation) [ send confirmation to peer in case of trigger ]
	TimeoutSubmitErrorCode  uint32 // Error code for SubmitSM packet in case of timeout
	TimeoutDeliverErrorCode uint32 // Error code for DeliverSM packet in case of timeout

	// Tracking RESP for TX (sent out) and RX (received) packets
	winMutex sync.RWMutex
	TrackTX  map[uint32]SMPPTracking
	TrackRX  map[uint32]SMPPTracking
	RXWindow uint32
	TXWindow uint32

	// Channel for INCOMING packets (SUBMIT_SM/DELIVER_SM/CANCEL_SM/REPLACE_SM/QUERY_SM)
	Inbox chan SMPPPacket

	// Channel for INCOMING ACK packets (_RESP)
	InboxR chan SMPPPacket

	// Outgoing messages and replies to SMPP link
	Outbox chan SMPPPacket
	// Outgoing RAW messages to SMPP link (only BIND_RESP and ENQUIRE_LINK/ENQUIRE_LINK_RESP packets)
	OutboxRAW chan []byte

	// SMPP Bind validator
	BindValidator  chan BindValidatorRequest
	BindValidatorR chan BindValidatorResponce

	//
	ManualBindValidate bool

	DebugLevel uint

	// Print-debug NetBuf in case of failure
	DebugNetBuf bool

	BufTX struct {
		NetTX        [libsmpp.SESSION_NET_BUF_SIZE]PacketNetBuf
		RingPosition int
		sync.RWMutex
	}

	BufRX struct {
		NetRX        [libsmpp.SESSION_NET_BUF_SIZE]PacketNetBuf
		RingPosition int
		sync.RWMutex
	}
}

type SMPPTracking struct {
	SeqNo      uint32    // SMPP Session SeqNo
	CommandID  uint32    // Original SMPP CommandID
	T          time.Time // Start tracking time
	CreateTime time.Time // Original packet create time

	UplinkTransactionID uint32 // Uniq packet ID, provided by UPLINK
}

// Historical buffer of processed (sent/received) packets
type PacketNetBuf struct {
	HDR      []byte
	Data     []byte
	DataSize uint32
	IsRaw    bool
}

// TLV Code
type TLVCode uint32

type SMPPAddress struct {
	TON  uint8  // source_addr_ton
	NPI  uint8  // source_addr_npi
	Addr string // source_addr C-Octet-String (max 21)
}

// SUBMIT_SM structure
type SMPPSubmit struct {
	ServiceType           string // service_type C-Octet-String (max 6)
	Source                SMPPAddress
	Dest                  SMPPAddress
	ESMClass              uint8  // esm_class
	ProtocolID            uint8  // protocol_id
	PriorityFlag          uint8  // priority_flag
	ScheduledDeliveryTime string // scheduled_delivery_type C-Octet-String (1 or 17)
	ValidityPeriod        string // validity_period  C-Octet-String (1 or 17)
	RegisteredDelivery    uint8  // registered_delivery
	ReplaceIfPresent      uint8  // replace_if_present
	DataCoding            uint8  // data_coding
	SmDefaultMsgID        uint8  // sm_default_msg_id
	SmLength              uint8  // sm_length
	ShortMessages         string // short_message (0-254)

	TLV map[TLVCode]interface{}
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
