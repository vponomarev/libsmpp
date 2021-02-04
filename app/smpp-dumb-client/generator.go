package main

import (
	"encoding/hex"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// Prepare SMPPSubmit structure for stress message generation
func prepareSubmit(config Config) (oP libsmpp.SMPPSubmit, err error) {
	// Prepare SUBMIT_SM packet if specified
	oP = libsmpp.SMPPSubmit{
		ServiceType: "",
		Source: libsmpp.SMPPAddress{
			TON:  uint8(config.Generator.Message.From.TON),
			NPI:  uint8(config.Generator.Message.From.NPI),
			Addr: config.Generator.Message.From.Addr,
		},
		Dest: libsmpp.SMPPAddress{
			TON:  uint8(config.Generator.Message.To.TON),
			NPI:  uint8(config.Generator.Message.To.NPI),
			Addr: config.Generator.Message.To.Addr,
		},
		ShortMessages:      config.Generator.Message.Body,
		ValidityPeriod:     config.Generator.Message.ValidityPeriod,
		RegisteredDelivery: uint8(config.Generator.Message.RegisteredDelivery),
		TLV:                map[libsmpp.TLVCode]libsmpp.TLVStruct{},
	}
	for _, tlv := range config.Generator.Message.TLV {
		tEntity := strings.Split(tlv, ";")
		if len(tEntity) != 3 {
			err = fmt.Errorf("error parsing TLV [%s] - should be 3 params", tlv)
			return
		}

		tVal := strings.Trim(tEntity[2], " ")
		if tVal[0] != '"' || tVal[len(tVal)-1] != '"' {
			err = fmt.Errorf("error parsing TLV [%s] - take value into quotes", tlv)
			return
		}
		tVal = strings.Trim(tVal, "\"")

		var tK int64
		var tV []byte
		if (len(tEntity[0]) > 2) && (tEntity[0][0:2] == "0x") {
			if tK, err = strconv.ParseInt(tEntity[0][2:], 16, 16); err != nil {
				err = fmt.Errorf("error parsing TLV [%s] - HEX key [%s]: %v", tlv, tEntity[0], err)
				return
			}
		} else {
			if tK, err = strconv.ParseInt(tEntity[0], 10, 16); err != nil {
				err = fmt.Errorf("error parsing TLV [%s] - DEC key [%s]: %v", tlv, tEntity[0], err)
				return
			}
		}
		switch strings.Trim(tEntity[1], " ") {
		case "string":
			tV = []byte(tVal)
		case "hex":
			if tV, err = hex.DecodeString(tVal); err != nil {
				err = fmt.Errorf("error parsing TLV [%s] - HEX value [%s]: %v", tlv, tEntity[2], err)
				return
			}
		case "dynamic":
			tlvDynamic = append(tlvDynamic, TLVDynamic{
				ID:       libsmpp.TLVCode(tK),
				Template: tVal,
			})
			fmt.Println("TLV [", tlv, "] KEY=", tK, "; DYNAMIC[", tVal, "]")
			continue
		default:
			err = fmt.Errorf("error parsing TLV [%s] - Unsupported value type [%s]: %v", tlv, tEntity[1], err)
			return
		}
		oP.TLV[libsmpp.TLVCode(tK)] = libsmpp.TLVStruct{
			Data: tV,
			Len:  uint16(len(tV)),
		}
		fmt.Println("TLV [", tlv, "] KEY=", tK, "; VAL[", tV, "]")

	}
	return
}

// Send messages
func PacketSender(s *libsmpp.SMPPSession, ps libsmpp.SMPPSubmit, tlvDynamic []TLVDynamic, config Config, TimeTracker *TrackProcessingTime, SendCompleteCH chan interface{}) {
	// Sleep for 3s after finishing sending and close trigger channel
	defer func(config Config) {
		if config.Generator.StayConnected {
			return
		} else {
			time.Sleep(5 * time.Second)
			close(SendCompleteCH)
		}
	}(config)

	var tick time.Duration
	var tickCounter uint
	var tickInfoModule uint
	var blockSize uint
	var msgLastSec uint

	// Encode packet for static generation
	p, rErr := s.EncodeSubmitSm(ps)
	if rErr != nil {
		log.WithFields(log.Fields{"type": "smpp-client", "service": "packetSender"}).Fatal("Error encoding static packet")
		return
	}

	// For rate > 1000 SMS/sec send send messages each 2 ms
	if config.Generator.SendRate > 1000 {
		tick = 2 * time.Millisecond
		blockSize = config.Generator.SendRate / 500
		tickInfoModule = 500
	} else if config.Generator.SendRate > 100 {
		// For rate > 100 SMS/sec send send messages each 10 ms
		tick = 10 * time.Millisecond
		blockSize = config.Generator.SendRate / 100
		tickInfoModule = 100
	} else if config.Generator.SendRate > 10 {
		// For rate > 10 SMS/sec AND <= 100 SMS/sec send messages each 100 ms
		tick = 100 * time.Millisecond
		blockSize = config.Generator.SendRate / 10
		tickInfoModule = 10
	} else if config.Generator.SendRate > 1 {
		// For rate > 1 SMS/sec AND <= 10 SMS/sec send message each 500 ms
		tick = 500 * time.Millisecond
		blockSize = config.Generator.SendRate / 2
		tickInfoModule = 2
	} else {
		// Rate is 1 SMS/sec
		tick = 1 * time.Second
		blockSize = config.Generator.SendRate
		tickInfoModule = 1
	}

	var done uint
	c := time.Tick(tick)
	lastInfoReport := time.Now()
	firstInfoReport := time.Now()
	for {
		select {
		case <-c:
			// First tick
			if tickCounter == 0 {
				lastInfoReport = time.Now()
				firstInfoReport = time.Now()

				// Don't sent anything during first tick
				tickCounter++
				continue
			}

			tickCounter++

			// Init block size for current tick
			tickBlock := blockSize

			// Skip current tick in case of overload
			txQ, rxQ := s.GetTrackQueueSize()
			var skipSend bool
			if config.Generator.SendWindow > 0 {
				if uint(txQ) >= config.Generator.SendWindow {
					skipSend = true
				}
				if tickBlock+uint(txQ) > config.Generator.SendWindow {
					tickBlock = config.Generator.SendWindow - uint(txQ)
				}
			}

			var i uint
			if !skipSend {
				for ; (i < tickBlock) && (done < config.Generator.SendCount); i++ {

					// Dynamically generate message if:
					// - TO is templated
					// - There are TLV Dynamic fields
					if config.Generator.Message.To.Template || (len(tlvDynamic) > 0) {
						// Prepare new packet instance
						pN := ps

						// Process TO number template
						if config.Generator.Message.To.Template {
							randRunes := []rune("0123456789")

							// Activate replacement only in case of template data
							if cRnd := strings.Count(config.Generator.Message.To.Addr, "#"); cRnd > 0 {
								da := config.Generator.Message.To.Addr
								for ; cRnd > 0; cRnd-- {
									da = strings.Replace(da, "#", string(randRunes[rand.Intn(len(randRunes))]), 1)
								}
								pN.Dest.Addr = da
							}
						}

						// Add DYNAMIC TLV fields
						for _, dV := range tlvDynamic {
							tData := dV.Template
							if strings.Contains(tData, "{timestamp}") {
								tData = strings.ReplaceAll(tData, "{timestamp}", strconv.FormatInt(time.Now().Unix(), 10))
							}

							pN.TLV[dV.ID] = libsmpp.TLVStruct{
								Data: []byte(tData),
								Len:  uint16(len(tData)),
							}
						}

						// Encode SubmitSM packet
						var rErr error
						p, rErr = s.EncodeSubmitSm(pN)
						if rErr != nil {
							log.WithFields(log.Fields{"type": "smpp-client", "service": "packetSender"}).Fatal("Error encoding packet body in message loop")
							return
						}

					}
					p.CreateTime = time.Now()

					s.Outbox <- p
					msgLastSec++
					done++
				}
			}
			if done >= config.Generator.SendCount {
				reportDiff := time.Since(firstInfoReport).Milliseconds()
				var realRate int64
				if reportDiff > 0 {
					realRate = (int64(config.Generator.SendCount) * 1000) / reportDiff
				}

				fmt.Println("#Finished sending", config.Generator.SendCount, "messages with expected rate:", config.Generator.SendRate, ", real rate:", realRate)
				return
			}

			if tickCounter%tickInfoModule == 0 {
				TimeTracker.Lock()
				tCnt := TimeTracker.Count
				tDur := TimeTracker.DelayTotal
				TimeTracker.Count = 0
				TimeTracker.DelayTotal = 0
				TimeTracker.Unlock()

				var tAvg int64
				if tCnt > 0 {
					if tCnt > 0 {
						tAvg = tDur.Microseconds() / int64(tCnt)
					}
				}

				reportDiff := time.Since(lastInfoReport).Milliseconds()
				if reportDiff < 1 {
					reportDiff = 1
				}
				lastInfoReport = time.Now()
				fmt.Println("[", s.SessionID, "] During last", reportDiff, "ms:", int64(msgLastSec)*1000/reportDiff, "[MAX:", done, "][TX:", txQ, "][RX:", rxQ, "][RTD avg micros: ", tAvg, ",", tCnt, "]")
				statsLog.Update(lastInfoReport, StatCounter{{ID: "SentRate", Value: uint32(int64(msgLastSec) * 1000 / reportDiff)}, {ID: "SentCount", Value: uint32(done)}, {ID: "SentRTD", Value: uint32(tAvg / 1000)}})

				msgLastSec = 0
			}

		case <-s.Closed:
			fmt.Println("#ClosedCH")
			return
		}
	}

}
