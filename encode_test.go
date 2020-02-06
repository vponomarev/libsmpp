package libsmpp

import (
	"fmt"
	"github.com/franela/goblin"
	libsmpp "libsmpp/const"
	"testing"
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
		g.It("[ENQUIRE_LINK] Encode packet", func() {
			expected := []byte{
				0x00, 0x00, 0x00, 0x10,
				0x00, 0x00, 0x00, 0x15,
				0x00, 0x00, 0x00, 0x00,
				0x12, 0x34, 0x56, 0x78,
			}
			s := SMPPSession{}
			res := s.EncodeEnquireLink(0x12345678)
			g.Assert(res).Equal(expected)
		})

		g.It("[ENQUIRE_LINK_RESP] Encode packet", func() {
			expected := []byte{
				0x00, 0x00, 0x00, 0x10,
				0x80, 0x00, 0x00, 0x15,
				0x00, 0x00, 0x00, 0x00,
				0x12, 0x34, 0x56, 0x78,
			}
			s := SMPPSession{}
			res := s.EncodeEnquireLinkResp(0x12345678)
			g.Assert(res).Equal(expected)
		})

		g.It("[BIND_RESP] Encode packet", func() {
			systemID := "ABCDEF876"
			expected := []byte{
				0x00, 0x00, 0x00, 0x1A, // Length
				0x80, 0x00, 0x00, 0x02, // Command ID
				0xfe, 0xdc, 0xba, 0x98, // Status
				0x12, 0x34, 0x56, 0x78, // Sequence
				0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x38, 0x37, 0x36, 0x00, // SystemID
			}
			s := SMPPSession{}
			res := s.EncodeBindResp(libsmpp.CMD_BIND_TRANSMITTER, 0x12345678, 0xFEDCBA98, systemID)
			g.Assert(res).Equal(expected)
		})

		g.It("[BIND_RESP] Decode packet", func() {
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
			g.Assert(rErr == nil).IsTrue()

			rState, rSystemID, rErr := s.DecodeBindResp(p, input[16:])
			g.Assert(uint32(0xfedcba98)).Equal(rState)
			g.Assert(rErr == nil).IsTrue()
			g.Assert(rSystemID).Equal(systemID)
		})

	})
}
