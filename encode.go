package libsmpp

import (
	"encoding/binary"
	"fmt"
	"libsmpp/const"
)

func ReadCString(b []byte, maxLen int, fieldName string) (data string, l int, err error) {
	if (maxLen < 1) || (maxLen > len(b)) {
		maxLen = len(b)
	}

	for l = 0; l < maxLen; l++ {
		if b[l] == 0 {
			if l > 0 {
				data = string(b[0:l])
			} else {
				data = ""
			}
			// Skip trailing 0x00
			l++
			return
		}
	}

	// No terminator found, copy the least part of the line and raise an error
	data = string(b[0:maxLen])
	err = fmt.Errorf("No CString terminator for field [%s]", fieldName)
	return
}

func (p *SMPPPacket) DecodeHDR(b []byte) error {
	if len(b) < 16 {
		return fmt.Errorf("Header is too short (%d, expecting 16 or mode bytes)", len(b))
	}
	p.Hdr.Len = binary.BigEndian.Uint32(b[0:])
	p.Hdr.ID = binary.BigEndian.Uint32(b[4:])
	p.Hdr.Status = binary.BigEndian.Uint32(b[8:])
	p.Hdr.Seq = binary.BigEndian.Uint32(b[12:])

	if p.Hdr.Len > MaxSMPPPacketSize {
		return fmt.Errorf("Packet body is too large (%d, allowed only %d bytes)", p.Hdr.Len, MaxSMPPPacketSize)
	}
	if p.Hdr.Len < 16 {
		return fmt.Errorf("Packet body is too short (%d)", p.Hdr.Len)
	}
	return nil
}

// Encode ENQUIRE_LINK
func (s *SMPPSession) EncodeEnquireLinkRAW(seq uint32) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint32(buf, 16)
	binary.BigEndian.PutUint32(buf[4:], libsmpp.CMD_ENQUIRE_LINK)
	binary.BigEndian.PutUint32(buf[8:], 0)
	binary.BigEndian.PutUint32(buf[12:], seq)
	return buf
}

// Encode ENQUIRE_LINK_RESP
func (s *SMPPSession) EncodeEnquireLinkRespRAW(seq uint32) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint32(buf, 16)
	binary.BigEndian.PutUint32(buf[4:], libsmpp.CMD_ENQUIRE_LINK_RESP)
	binary.BigEndian.PutUint32(buf[8:], 0)
	binary.BigEndian.PutUint32(buf[12:], seq)
	return buf
}

// Encode BindResp
func (s *SMPPSession) EncodeBindRespRAW(ID uint32, seq uint32, status uint32, systemID string) []byte {
	buf := make([]byte, MaxSMPPPacketSize)

	pl := uint32(16 + len(systemID) + 1)

	binary.BigEndian.PutUint32(buf, pl)
	binary.BigEndian.PutUint32(buf[4:], ID+0x80000000)
	binary.BigEndian.PutUint32(buf[8:], status)
	binary.BigEndian.PutUint32(buf[12:], seq)
	copy(buf[16:16+len(systemID)], []byte(systemID))
	buf[pl] = 0

	return buf[0:pl]
}

// Decode BindResp
func (s *SMPPSession) DecodeBindResp(p *SMPPPacket) (state uint32, SystemID string, err error) {
	state = p.Hdr.Status
	if p.Hdr.Len == 16 {
		return
	}
	SystemID, _, err = ReadCString(p.Body, len(p.Body), "SystemID")
	return
}

// Generate SubmitSM Resp packet
func (s *SMPPSession) EncodeSubmitSmResp(p SMPPPacket, status uint32, msgID string) (pr SMPPPacket) {
	pr = SMPPPacket{
		Hdr: SMPPHeader{
			ID:     libsmpp.CMD_SUBMIT_SM_RESP,
			Status: status,
			Seq:    p.Hdr.Seq,
		},
		BodyLen:     uint32(len(msgID) + 1),
		SeqComplete: true,
	}
	pr.Body = make([]byte, len(msgID)+1)
	copy(pr.Body, msgID)
	return
}

// Generate DeliverSM Resp packet
func (s *SMPPSession) EncodeDeliverSmResp(p SMPPPacket, status uint32) (pr SMPPPacket) {
	pr = SMPPPacket{
		Hdr: SMPPHeader{
			ID:     libsmpp.CMD_DELIVER_SM_RESP,
			Status: status,
			Seq:    p.Hdr.Seq,
		},
		BodyLen:     1,
		SeqComplete: true,
	}
	pr.Body = make([]byte, 1)
	pr.Body[0] = 0
	return
}

// Generate GENERIC_NACK
func (s *SMPPSession) EncodeGenericNack(p SMPPPacket, status uint32) (pr SMPPPacket) {
	pr = SMPPPacket{
		Hdr: SMPPHeader{
			ID:     libsmpp.CMD_GENERIC_NACK,
			Status: status,
			Seq:    p.Hdr.Seq,
		},
		BodyLen:     0,
		SeqComplete: true,
	}
	return
}

func (s *SMPPSession) DecodeBind(p *SMPPPacket) error {
	// Validate correct command ID
	switch p.Hdr.ID {
	case libsmpp.CMD_BIND_RECEIVER:
		s.Cs.sm = CSMPPRX
	case libsmpp.CMD_BIND_TRANSMITTER:
		s.Cs.sm = CSMPPTX
	case libsmpp.CMD_BIND_TRANSCIEVER:
		s.Cs.sm = CSMPPTRX
	default:
		return fmt.Errorf("Unsupported bind commaind ID [%d]", p.Hdr.ID)
	}
	s.Bind.ConnMode = s.Cs.sm

	// Decode systemID
	var l, offset int
	var erx error
	offset = 0
	s.Bind.SystemID, l, erx = ReadCString(p.Body[offset:], 16, "SystemID")
	offset += l
	if erx != nil {
		return erx
	}

	s.Bind.Password, l, erx = ReadCString(p.Body[offset:], 9, "Password")
	offset += l
	if erx != nil {
		return erx
	}

	s.Bind.SystemType, l, erx = ReadCString(p.Body[offset:], 13, "SystemType")
	offset += l
	if erx != nil {
		return erx
	}

	if offset >= int(p.BodyLen) {
		return fmt.Errorf("Invalid packet, no data for InterfaceVersion")
	}

	// Interface version
	s.Bind.IVersion = uint(p.Body[offset])
	offset++

	if offset >= int(p.BodyLen) {
		return fmt.Errorf("Invalid packet, no data for AddrTON")
	}

	// AddrTON
	s.Bind.AddrTON = uint(p.Body[offset])
	offset++
	if offset > int(p.BodyLen) {
		return fmt.Errorf("Invalid packet, no data for AddrNPI")
	}

	// AddrNPI
	s.Bind.AddrNPI = uint(p.Body[offset])
	offset++
	if offset >= int(p.BodyLen) {
		return fmt.Errorf("Invalid packet, no data for AddressRange")
	}

	s.Bind.AddrRange, l, erx = ReadCString(p.Body[offset:], 13, "AddressRange")
	offset += l

	if offset != int(p.BodyLen) {
		return fmt.Errorf("Invalid packet body len [HDR: %d, Context: %d]", p.BodyLen, offset)
	}

	return nil
}

// Generate BIND packet
func (s *SMPPSession) EncodeBind(m ConnSMPPMode, b SMPPBind) (p SMPPPacket, err error) {
	var cmdid uint32
	switch m {
	case CSMPPTX:
		cmdid = libsmpp.CMD_BIND_TRANSMITTER
	case CSMPPRX:
		cmdid = libsmpp.CMD_BIND_RECEIVER
	case CSMPPTRX:
		cmdid = libsmpp.CMD_BIND_TRANSCIEVER
	default:
		return SMPPPacket{}, fmt.Errorf("Invalid connection mode")
	}

	//
	// Validate max entity len
	if len(b.SystemID) > 15 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [SystemID]: %d, maxLen = 16", len(b.SystemID))
	}
	if len(b.Password) > 8 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [Password]: %d, maxLen = 9", len(b.Password))
	}
	if len(b.SystemType) > 12 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [SystemType]: %d, maxLen = 13", len(b.SystemType))
	}
	if len(b.AddrRange) > 40 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [AddressRange]: %d, maxLen = 41", len(b.SystemType))
	}

	//
	// Generate packet
	buf := make([]byte, MaxSMPPPacketSize)
	offset := 0
	// SystemID
	copy(buf, b.SystemID)
	offset += len(b.SystemID)
	buf[offset] = 0
	offset++

	copy(buf[offset:], b.Password)
	offset += len(b.Password)
	buf[offset] = 0
	offset++

	copy(buf[offset:], b.SystemType)
	offset += len(b.SystemType)
	buf[offset] = 0
	offset++

	buf[offset] = byte(b.IVersion)
	offset++
	buf[offset] = byte(b.AddrTON)
	offset++
	buf[offset] = byte(b.AddrNPI)
	offset++
	copy(buf[offset:], b.AddrRange)
	offset += len(b.AddrRange)
	buf[offset] = 0
	offset++

	p = SMPPPacket{
		Hdr:     SMPPHeader{ID: cmdid},
		BodyLen: uint32(offset),
		Body:    make([]byte, offset),
	}
	copy(p.Body, buf[0:offset])

	return p, nil
}

// Input:
// ss SMPPSubmit - structure for SubmitSM/DeliverSM
// Output:
// p SMPPPacket - encoded SMPP packet
// c uint32 - SMPP Error code if present
// err error - error description
func (s *SMPPSession) EncodeSubmitDeliverSm(CMD uint32, ss SMPPSubmit) (p SMPPPacket, err error) {
	// Validate max entity len
	if len(ss.ServiceType) > 5 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [service_type]: %d, maxLen = 5", len(ss.ServiceType))
	}
	if len(ss.Source.Addr) > 20 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [source_addr]: %d, maxLen = 20", len(ss.Source.Addr))
	}
	if len(ss.Dest.Addr) > 20 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [dest_addr]: %d, maxLen = 20", len(ss.Dest.Addr))
	}
	if len(ss.ScheduledDeliveryTime) > 16 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [scheduled_delivery_time]: %d, maxLen = 16", len(ss.ScheduledDeliveryTime))
	}
	if len(ss.ValidityPeriod) > 16 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [validity_period]: %d, maxLen = 16", len(ss.ValidityPeriod))
	}
	if len(ss.ShortMessages) > 254 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [short_message]: %d, maxLen = 254", len(ss.ShortMessages))
	}

	switch CMD {
	case libsmpp.CMD_SUBMIT_SM, libsmpp.CMD_DELIVER_SM:
		p.Hdr.ID = CMD
	default:
		return SMPPPacket{}, fmt.Errorf("Invalid CommandID: %x", CMD)
	}

	//
	// Generate packet
	buf := make([]byte, MaxSMPPPacketSize)
	offset := 0

	// ServiceType
	copy(buf, ss.ServiceType)
	offset += len(ss.ServiceType)
	buf[offset] = 0
	offset++

	// Source Address TON
	buf[offset] = ss.Source.TON
	offset++

	// Source Address NPI
	buf[offset] = ss.Source.NPI
	offset++

	// Source Addr
	copy(buf[offset:], ss.Source.Addr)
	offset += len(ss.Source.Addr)
	buf[offset] = 0
	offset++

	// Dest Address TON
	buf[offset] = ss.Dest.TON
	offset++

	// Dest Address NPI
	buf[offset] = ss.Dest.NPI
	offset++

	// Dest Addr
	copy(buf[offset:], ss.Dest.Addr)
	offset += len(ss.Dest.Addr)
	buf[offset] = 0
	offset++

	// ESM Class
	buf[offset] = ss.ESMClass
	offset++

	// Protocol ID
	buf[offset] = ss.ProtocolID
	offset++

	// Priority Flag
	buf[offset] = ss.PriorityFlag
	offset++

	// Scheduled Delivery Time
	copy(buf[offset:], ss.ScheduledDeliveryTime)
	offset += len(ss.ScheduledDeliveryTime)
	buf[offset] = 0
	offset++

	// Validity Period
	copy(buf[offset:], ss.ValidityPeriod)
	offset += len(ss.ValidityPeriod)
	buf[offset] = 0
	offset++

	// Registered Delivery
	buf[offset] = ss.RegisteredDelivery
	offset++

	// Replace If Present
	buf[offset] = ss.ReplaceIfPresent
	offset++

	// Data Coding
	buf[offset] = ss.DataCoding
	offset++

	// SM Default MSG ID
	buf[offset] = ss.SmDefaultMsgID
	offset++

	// SM Length
	buf[offset] = uint8(len(ss.ShortMessages))
	offset++

	// Short Message
	copy(buf[offset:], ss.ShortMessages)
	offset += len(ss.ShortMessages)

	// Publish TLV if it's set
	if ss.TLV != nil {
		for tK, tV := range ss.TLV {
			binary.BigEndian.PutUint16(buf[offset:], uint16(tK))
			offset += 2
			binary.BigEndian.PutUint16(buf[offset:], tV.Len)
			offset += 2
			copy(buf[offset:offset+int(tV.Len)], tV.Data)
			offset += int(tV.Len)
		}
	}

	p.Body = buf[0:offset]
	p.BodyLen = uint32(offset)

	// DONE!
	return
}

func (s *SMPPSession) EncodeSubmitSm(ss SMPPSubmit) (p SMPPPacket, err error) {
	return s.EncodeSubmitDeliverSm(libsmpp.CMD_SUBMIT_SM, ss)
}

func (s *SMPPSession) EncodeDeliverSm(ss SMPPSubmit) (p SMPPPacket, err error) {
	return s.EncodeSubmitDeliverSm(libsmpp.CMD_DELIVER_SM, ss)
}

func (s *SMPPSession) DecodeSubmitDeliverSm(p *SMPPPacket) (ss SMPPSubmit, err error) {
	// Validate correct command ID
	switch p.Hdr.ID {
	case libsmpp.CMD_SUBMIT_SM, libsmpp.CMD_DELIVER_SM:
	default:
		err = fmt.Errorf("Unsupported command ID [%d]", p.Hdr.ID)
		return
	}

	// serviceType
	var l, offset int
	offset = 0
	ss.ServiceType, l, err = ReadCString(p.Body[offset:], 6, "service_type")
	offset += l
	if err != nil {
		return
	}

	if uint32(offset+3) > p.BodyLen {
		err = fmt.Errorf("Invalid packet, no data for Source Addr Info")
		return
	}

	// Source Addr TON
	ss.Source.TON = p.Body[offset]
	offset++

	// Source Addr NPI
	ss.Source.NPI = p.Body[offset]
	offset++

	// Source Addr NPI
	ss.Source.Addr, l, err = ReadCString(p.Body[offset:], 21, "source_addr")
	offset += l
	if err != nil {
		return
	}

	if uint32(offset+3) > p.BodyLen {
		err = fmt.Errorf("Invalid packet, no data for Dest Addr Info")
		return
	}

	// Dest Addr TON
	ss.Dest.TON = p.Body[offset]
	offset++

	// Dest Addr NPI
	ss.Dest.NPI = p.Body[offset]
	offset++

	// Dest Addr
	ss.Dest.Addr, l, err = ReadCString(p.Body[offset:], 21, "dest_addr")
	offset += l
	if err != nil {
		return
	}

	if uint32(offset+4) > p.BodyLen {
		err = fmt.Errorf("Invalid packet, no data for Scheduled Delivery Time")
		return
	}

	ss.ESMClass = p.Body[offset]
	offset++

	ss.ProtocolID = p.Body[offset]
	offset++

	ss.PriorityFlag = p.Body[offset]
	offset++

	// Scheduled delivery time
	ss.ScheduledDeliveryTime, l, err = ReadCString(p.Body[offset:], 21, "scheduled_delivery_time")
	offset += l
	if err != nil {
		return
	}

	if uint32(offset+1) > p.BodyLen {
		err = fmt.Errorf("Invalid packet, no data for Validity Period")
		return
	}

	// Validity period
	ss.ValidityPeriod, l, err = ReadCString(p.Body[offset:], 21, "validity_period")
	offset += l
	if err != nil {
		return
	}

	if uint32(offset+4) > p.BodyLen {
		err = fmt.Errorf("Invalid packet, no data for sm_length")
		return
	}

	ss.RegisteredDelivery = p.Body[offset]
	offset++

	ss.ReplaceIfPresent = p.Body[offset]
	offset++

	ss.DataCoding = p.Body[offset]
	offset++

	ss.SmDefaultMsgID = p.Body[offset]
	offset++

	ss.SmLength = p.Body[offset]
	offset++

	// Load message body
	if ss.SmLength > 0 {
		if uint32(offset)+uint32(ss.SmLength) > p.BodyLen {
			err = fmt.Errorf("Invalid packet, no data for short_message")
			return
		}
		ss.ShortMessages = string(p.Body[offset : offset+int(ss.SmLength)])
	}

	// TODO: Parse TLV Data

	return
}
