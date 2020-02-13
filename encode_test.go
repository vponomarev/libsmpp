package libsmpp

import (
	"fmt"
	"github.com/franela/goblin"
	libsmpp "libsmpp/const"
	"testing"
	//	"time"
)

func TestEncodeEnquireLink(t *testing.T) {
	g := goblin.Goblin(t)

	g.Describe("General function tests", func() {
		g.It("ReadCString - correct", func() {
			expected := "This is string"
			input := []byte(expected)
			input = append(input, []byte{0, 0x12, 0x25, 0x16}...)
			//s := SMPPSession{}
			res, l, err := ReadCString(input, 20, "TestVar")
			g.Assert(res).Equal(expected)
			g.Assert(err).Equal(nil)
			g.Assert(l).Equal(len(expected) + 1)
		})

		g.It("ReadCString - truncate", func() {
			expected := "This is string"
			truncated := "This is"
			input := []byte(expected)
			input = append(input, []byte{0, 0x12, 0x25, 0x16}...)
			res, l, err := ReadCString(input, len(truncated), "TestVar")
			g.Assert(res).Equal(truncated)
			g.Assert(l).Equal(len(truncated))
			g.Assert(err == nil).IsFalse()
		})

		g.It("ReadCString - empty", func() {
			input := []byte{}
			res, l, err := ReadCString(input, 20, "TestVar")
			g.Assert(res).Equal("")
			g.Assert(l).Equal(0)
			g.Assert(err == nil).IsFalse()
		})

		g.It("DecodeHDR - short packet", func() {
			input := []byte{0x00, 0x00, 0x00, 0x00}
			p := &SMPPPacket{}
			err := p.DecodeHDR(input)
			g.Assert(err != nil).IsTrue()
		})

		g.It("DecodeHDR - HDR Len is too large", func() {
			input := []byte{0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
			p := &SMPPPacket{}
			err := p.DecodeHDR(input)
			g.Assert(err).Equal(fmt.Errorf("Packet body is too large (1048576, allowed only 8192 bytes)"))
		})

		g.It("DecodeHDR - HDR Len is too short", func() {
			input := []byte{0x00, 0x00, 0x00, 0x0F, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
			p := &SMPPPacket{}
			err := p.DecodeHDR(input)
			g.Assert(err).Equal(fmt.Errorf("Packet body is too short (15)"))
		})

		g.It("DecodeHDR - Decode test", func() {
			input := []byte{
				0x00, 0x00, 0x00, 0x1A, // Length
				0x80, 0x00, 0x00, 0x02, // Command ID
				0xfe, 0xdc, 0xba, 0x98, // Status
				0x12, 0x34, 0x56, 0x78, // Sequence
				0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x38, 0x37, 0x36, 0x00, // SystemID
			}

			p := &SMPPPacket{}
			err := p.DecodeHDR(input)
			g.Assert(err == nil).IsTrue()
			g.Assert(p.Hdr.Len).Equal(uint32(0x1a))
			g.Assert(p.Hdr.Seq).Equal(uint32(0x12345678))
			g.Assert(p.Hdr.Status).Equal(uint32(0xfedcba98))
			g.Assert(p.Hdr.ID).Equal(uint32(0x80000002))
		})

	})

	g.Describe("Test of packet generation functions", func() {
		g.It("[ENQUIRE_LINK] Encode RAW packet", func() {
			expected := []byte{
				0x00, 0x00, 0x00, 0x10,
				0x00, 0x00, 0x00, 0x15,
				0x00, 0x00, 0x00, 0x00,
				0x12, 0x34, 0x56, 0x78,
			}
			s := SMPPSession{}
			res := s.EncodeEnquireLinkRAW(0x12345678)
			g.Assert(res).Equal(expected)
		})

		g.It("[ENQUIRE_LINK_RESP] Encode RAW packet", func() {
			expected := []byte{
				0x00, 0x00, 0x00, 0x10,
				0x80, 0x00, 0x00, 0x15,
				0x00, 0x00, 0x00, 0x00,
				0x12, 0x34, 0x56, 0x78,
			}
			s := SMPPSession{}
			res := s.EncodeEnquireLinkRespRAW(0x12345678)
			g.Assert(res).Equal(expected)
		})

		g.It("[BIND_RESP] Encode RAW packet", func() {
			systemID := "ABCDEF876"
			expected := []byte{
				0x00, 0x00, 0x00, 0x1A, // Length
				0x80, 0x00, 0x00, 0x02, // Command ID
				0xfe, 0xdc, 0xba, 0x98, // Status
				0x12, 0x34, 0x56, 0x78, // Sequence
				0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x38, 0x37, 0x36, 0x00, // SystemID
			}
			s := SMPPSession{}
			res := s.EncodeBindRespRAW(libsmpp.CMD_BIND_TRANSMITTER, 0x12345678, 0xFEDCBA98, systemID)
			g.Assert(res).Equal(expected)
		})

		g.It("[BIND_RESP] Decode RAW packet", func() {
			systemID := "ABCDEF876"
			input := []byte{
				0x00, 0x00, 0x00, 0x1A, // Length
				0x80, 0x00, 0x00, 0x02, // Command ID
				0xfe, 0xdc, 0xba, 0x98, // Status
				0x12, 0x34, 0x56, 0x78, // Sequence
				0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x38, 0x37, 0x36, 0x00, // SystemID
			}
			s := SMPPSession{}
			p := &SMPPPacket{}
			rErr := p.DecodeHDR(input)
			p.Body = input[16:]
			g.Assert(rErr == nil).IsTrue()

			rState, rSystemID, rErr := s.DecodeBindResp(p)
			g.Assert(uint32(0xfedcba98)).Equal(rState)
			g.Assert(rErr == nil).IsTrue()
			g.Assert(rSystemID).Equal(systemID)
		})

		g.It("[SUBMIT_SM_RESP] Encode packet", func() {
			expected := SMPPPacket{
				Hdr: SMPPHeader{
					ID:     0x80000004,
					Status: 0x12345678,
					Seq:    0xabcdef01,
				},
				Body:        []byte("MsgIDInfo\x00"),
				BodyLen:     10,
				SeqComplete: true,
			}
			s := SMPPSession{}
			p := SMPPPacket{
				Hdr: SMPPHeader{
					Seq: 0xabcdef01,
				},
			}
			rP := s.EncodeSubmitSmResp(p, 0x12345678, "MsgIDInfo")
			g.Assert(expected).Equal(rP)
		})

		g.It("[DELIVER_SM_RESP] Encode packet", func() {
			expected := SMPPPacket{
				Hdr: SMPPHeader{
					ID:     0x80000005,
					Status: 0x12345678,
					Seq:    0xabcdef01,
				},
				Body:        []byte("\x00"),
				BodyLen:     1,
				SeqComplete: true,
			}
			s := SMPPSession{}
			p := SMPPPacket{
				Hdr: SMPPHeader{
					Seq: 0xabcdef01,
				},
			}
			rP := s.EncodeDeliverSmResp(p, 0x12345678)
			g.Assert(expected).Equal(rP)
		})

		g.It("[GENERIC_NACK] Encode packet", func() {
			expected := SMPPPacket{
				Hdr: SMPPHeader{
					ID:     0x80000000,
					Status: 0x12345678,
					Seq:    0xabcdef01,
				},
				SeqComplete: true,
			}
			s := SMPPSession{}
			p := SMPPPacket{
				Hdr: SMPPHeader{
					Seq: 0xabcdef01,
				},
			}
			rP := s.EncodeGenericNack(p, 0x12345678)
			g.Assert(expected).Equal(rP)
		})
	})

	g.Describe("BIND: DecodeBind() function test", func() {
		g.It("Command ID validation", func() {
			s := SMPPSession{}
			p := SMPPPacket{
				Hdr: SMPPHeader{
					ID: 758456697,
				},
			}
			err := s.DecodeBind(&p)
			g.Assert(err).Equal(fmt.Errorf("Unsupported bind commaind ID [758456697]"))
		})

		g.It("Packet decode", func() {
			s := SMPPSession{}
			p := SMPPPacket{
				Hdr: SMPPHeader{
					ID: 0x00000001,
				},
				Body: []byte("SystemID0987654\x00password\x00system_type*\x00\x34\x01\x02ARDFC\x00"),
			}
			p.Hdr.Len = uint32(16 + len(p.Body))
			p.BodyLen = uint32(len(p.Body))
			rErr := s.DecodeBind(&p)
			g.Assert(rErr).Equal(nil)
			g.Assert(s.Bind.SystemID).Equal("SystemID0987654")
			g.Assert(s.Bind.Password).Equal("password")
			g.Assert(s.Bind.SystemType).Equal("system_type*")
			g.Assert(s.Bind.AddrTON).Equal(uint(1))
			g.Assert(s.Bind.AddrNPI).Equal(uint(2))
			g.Assert(s.Bind.AddrRange).Equal("ARDFC")
		})
	})

	g.Describe("BIND: EncodeBind() function test", func() {
		g.It("Command ID validation", func() {
			s := SMPPSession{}
			_, rErr := s.EncodeBind(99, SMPPBind{})
			g.Assert(rErr).Equal(fmt.Errorf("Invalid connection mode"))
		})

		g.It("Packet encode", func() {
			s := SMPPSession{}
			input := SMPPBind{
				ConnMode:   0,
				SystemID:   "SystemID",
				Password:   "Password",
				SystemType: "SystemType",
				IVersion:   0x34,
				AddrTON:    0x42,
				AddrNPI:    0x16,
				AddrRange:  "ThisIsAddressRange",
				SMSCID:     "",
			}
			expected := []byte("SystemID\x00Password\x00SystemType\x00\x34\x42\x16ThisIsAddressRange\x00")
			rP, rErr := s.EncodeBind(CSMPPTX, input)
			g.Assert(rErr).Equal(nil)
			g.Assert(rP.Hdr.ID).Equal(uint32(2))
			g.Assert(rP.Body).Equal(expected)
		})
	})

	g.Describe("SUBMIT_SM/DELIVER_SM: EncodeSubmitDeliverSm() function test", func() {
		g.It("Input data validation", func() {
			s := SMPPSession{}
			input := SMPPSubmit{
				ServiceType: "ThisIsLongServiceType",
			}
			_, eRrr := s.EncodeSubmitSm(input)
			g.Assert(eRrr).Equal(fmt.Errorf("Invalid length of [service_type]: 21, maxLen = 5"))

			input = SMPPSubmit{
				Source: SMPPAddress{
					Addr: "AddressIsTooLong789012",
				},
			}

			_, eRrr = s.EncodeSubmitSm(input)
			g.Assert(eRrr).Equal(fmt.Errorf("Invalid length of [source_addr]: 22, maxLen = 20"))

			input = SMPPSubmit{
				Source: SMPPAddress{
					Addr: "Addr",
				},
			}

			_, eRrr = s.EncodeSubmitDeliverSm(libsmpp.CMD_BIND_TRANSCIEVER, input)
			g.Assert(eRrr).Equal(fmt.Errorf("Invalid CommandID: 9"))

		})

		g.It("Packet encode", func() {
			s := SMPPSession{}
			input := SMPPSubmit{
				ServiceType: "VDX",
				Source: SMPPAddress{
					5,
					2,
					"InfoAddrSt",
				},
				Dest: SMPPAddress{
					1,
					0,
					"79031234567",
				},
				ESMClass:              0x23,
				ProtocolID:            0x7d,
				PriorityFlag:          0xde,
				ScheduledDeliveryTime: "1234567890123456",
				ValidityPeriod:        "6543210987654321",
				RegisteredDelivery:    0x55,
				ReplaceIfPresent:      0x66,
				DataCoding:            0x77,
				SmDefaultMsgID:        0x88,
				ShortMessages:         "Test Message For SubmitSM coding",
			}
			expected := []byte("VDX\x00\x05\x02InfoAddrSt\x00\x01\x0079031234567\x00\x23\x7d\xde1234567890123456\x006543210987654321\x00\x55\x66\x77\x88\x20Test Message For SubmitSM coding")
			rP, eRrr := s.EncodeSubmitDeliverSm(libsmpp.CMD_SUBMIT_SM, input)
			g.Assert(eRrr).Equal(nil)
			g.Assert(rP.Hdr.ID).Equal(uint32(0x04))
			g.Assert(rP.Body).Equal(expected)
		})
	})
}
