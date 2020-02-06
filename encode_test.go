package libsmpp

import (
	"github.com/franela/goblin"
	"testing"
)

func TestEncodeEnquireLink(t *testing.T) {
	g := goblin.Goblin(t)
	g.Describe("#EncodeEnquireLink", func() {
		g.It("Check correct EnquireLink generation", func() {
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

		g.It("Check correct EnquireLinkResp generation", func() {
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
	})
}
