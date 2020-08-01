package libsmpp

import (
	//	"fmt"
	"github.com/franela/goblin"
	"testing"
)

func TestHTTPFunctions(t *testing.T) {
	g := goblin.Goblin(t)

	g.Describe("HTTP Encode tests", func() {
		g.It("Encode simple packet", func() {
			expected := "{\"ServiceType\":\"Service\",\"Source\":{\"TON\":5,\"NPI\":0,\"Addr\":\"TestMSG\"},\"Dest\":{\"TON\":1,\"NPI\":1,\"Addr\":\"79037011111\"},\"ESMClass\":5,\"ProtocolID\":6,\"PriorityFlag\":7,\"ScheduledDeliveryTime\":\"SDT\",\"ValidityPeriod\":\"VP\",\"RegisteredDelivery\":1,\"ReplaceIfPresent\":1,\"DataCoding\":8,\"SmDefaultMsgID\":0,\"SmLength\":0,\"ShortMessages\":\"MSG Content\",\"TLV\":null}"

			s := SMPPSubmit{
				ServiceType: "Service",
				Source: SMPPAddress{
					TON:  5,
					NPI:  0,
					Addr: "TestMSG",
				},
				Dest: SMPPAddress{
					TON:  1,
					NPI:  1,
					Addr: "79037011111",
				},
				ESMClass:              5,
				ProtocolID:            6,
				PriorityFlag:          7,
				ScheduledDeliveryTime: "SDT",
				ValidityPeriod:        "VP",
				RegisteredDelivery:    1,
				ReplaceIfPresent:      1,
				DataCoding:            8,
				SmDefaultMsgID:        0,
				SmLength:              0,
				ShortMessages:         "MSG Content",
			}

			res, err := s.toJSON()
			g.Assert(string(res)).Equal(expected)
			g.Assert(err).Equal(nil)
		})

		g.It("Encode packet with TLV's", func() {
			expected := "{\"ServiceType\":\"Service\",\"Source\":{\"TON\":5,\"NPI\":0,\"Addr\":\"TestMSG\"},\"Dest\":{\"TON\":1,\"NPI\":1,\"Addr\":\"79037011111\"},\"ESMClass\":5,\"ProtocolID\":6,\"PriorityFlag\":7,\"ScheduledDeliveryTime\":\"SDT\",\"ValidityPeriod\":\"VP\",\"RegisteredDelivery\":1,\"ReplaceIfPresent\":1,\"DataCoding\":8,\"SmDefaultMsgID\":0,\"SmLength\":0,\"ShortMessages\":\"MSG Content\",\"TLV\":{\"37\":{\"Data\":\"AAECAw==\",\"Len\":4}}}"

			s := SMPPSubmit{
				ServiceType: "Service",
				Source: SMPPAddress{
					TON:  5,
					NPI:  0,
					Addr: "TestMSG",
				},
				Dest: SMPPAddress{
					TON:  1,
					NPI:  1,
					Addr: "79037011111",
				},
				ESMClass:              5,
				ProtocolID:            6,
				PriorityFlag:          7,
				ScheduledDeliveryTime: "SDT",
				ValidityPeriod:        "VP",
				RegisteredDelivery:    1,
				ReplaceIfPresent:      1,
				DataCoding:            8,
				SmDefaultMsgID:        0,
				SmLength:              0,
				ShortMessages:         "MSG Content",
				TLV: map[TLVCode]TLVStruct{
					TLVCode(0x25): {
						Data: []byte("\x00\x01\x02\x03"),
						Len:  4,
					},
				},
			}

			res, err := s.toJSON()
			g.Assert(string(res)).Equal(expected)
			g.Assert(err).Equal(nil)
		})

	})

}
