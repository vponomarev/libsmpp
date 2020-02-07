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
func (s *SMPPSession) DecodeBindResp(p *SMPPPacket, b []byte) (state uint32, SystemID string, err error) {
	state = p.Hdr.Status
	if p.Hdr.Len == 16 {
		return
	}
	SystemID, _, err = ReadCString(b, len(b), "SystemID")
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
			ID:     libsmpp.CMD_DELIVER_SM,
			Status: status,
			Seq:    p.Hdr.Seq,
		},
		BodyLen:     0,
		SeqComplete: true,
	}
	return
}

func (s *SMPPSession) DecodeBind(p *SMPPPacket, b []byte) error {
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

	// Decode systemID
	var l, offset int
	var erx error
	offset = 16
	s.Bind.SystemID, l, erx = ReadCString(b[offset:], 16, "SystemID")
	offset += l
	if erx != nil {
		return erx
	}

	s.Bind.Password, l, erx = ReadCString(b[offset:], 9, "Password")
	offset += l
	if erx != nil {
		return erx
	}

	s.Bind.SystemType, l, erx = ReadCString(b[offset:], 13, "SystemType")
	offset += l
	if erx != nil {
		return erx
	}

	if offset >= int(p.Hdr.Len) {
		return fmt.Errorf("Invalid packet, no data for InterfaceVersion")
	}

	// Interface version
	s.Bind.IVersion = uint(b[offset])
	offset++

	if offset >= int(p.Hdr.Len) {
		return fmt.Errorf("Invalid packet, no data for AddrTON")
	}

	// AddrTON
	s.Bind.AddrTON = uint(b[offset])
	offset++
	if offset > int(p.Hdr.Len) {
		return fmt.Errorf("Invalid packet, no data for AddrNPI")
	}

	// AddrNPI
	s.Bind.AddrNPI = uint(b[offset])
	offset++
	if offset > int(p.Hdr.Len) {
		return fmt.Errorf("Invalid packet, no data for AddressRange")
	}

	s.Bind.AddrRange, l, erx = ReadCString(b[offset:], 13, "AddressRange")
	offset += l

	if offset != int(p.Hdr.Len) {
		return fmt.Errorf("Invalid packet body len [HDR: %d, Context: %d]", p.Hdr.Len, offset)
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
	}

	//
	// Validate max entity len
	if len(b.SystemID) > 16 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [SystemID]: %d, maxLen = 16", len(b.SystemID))
	}
	if len(b.Password) > 9 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [Password]: %d, maxLen = 9", len(b.Password))
	}
	if len(b.SystemType) > 13 {
		return SMPPPacket{}, fmt.Errorf("Invalid length of [SystemType]: %d, maxLen = 13", len(b.SystemType))
	}
	if len(b.AddrRange) > 41 {
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
