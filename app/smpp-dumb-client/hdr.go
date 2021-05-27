package main

import (
	"github.com/vponomarev/libsmpp"
	"net"
	"sync"
	"time"
)

type Config struct {
	DebugFeatures struct {
		HTTPIncoming bool `yaml:"httpincoming"`
	}
	Log struct {
		Level  string `yaml:"level,omitempty"`
		Rate   bool
		Netbuf bool `yaml:"netbuf"`
	}

	SMPP struct {
		Remote string `yaml:"remote,omitempty"`
		Bind   struct {
			SystemID   string `yaml:"systemID,omitempty"`
			SystemType string `yaml:"systemType,omitempty"`
			Password   string `yaml:"password,omitempty"`
			Mode       string `yaml:"mode,omitempty"`
		}
	}

	HTTP struct {
		Incoming struct {
			Listen  string `yaml:"listen"`
			URL     string `yaml:"url"`
			Enabled bool   `yaml:"enabled"`
		}
		Outgoing struct {
			URL     string `yaml:"url"`
			Enabled bool   `yaml:"enabled"`
		}
	}

	Profiler struct {
		Enabled bool   `yaml:"enabled"`
		Listen  string `yaml:"listen,omitempty"`
	}

	// Message generator configuration
	Generator struct {
		Enabled bool `yaml:"enabled"`
		Message struct {
			From struct {
				TON  int    `yaml:"ton"`
				NPI  int    `yaml:"npi"`
				Addr string `yaml:"addr"`
			}
			To struct {
				TON      int    `yaml:"ton"`
				NPI      int    `yaml:"npi"`
				Addr     string `yaml:"addr"`
				Template bool   `yaml:"template"`
			}
			RegisteredDelivery int      `yaml:"registeredDelivery"`
			ValidityPeriod     string   `yaml:"validityPeriod"`
			DataCoding         int      `yaml:"dataCoding"`
			Body               string   `yaml:"body"`
			TLV                []string `yaml:"tlv"`
		}
		SendCount  uint `yaml:"count" envconfig:"SEND_COUNT"`
		SendRate   uint `yaml:"rate" envconfig:"SEND_RATE"`
		SendWindow uint `yaml:"window" envconfig:"SEND_WINDOW"`

		StayConnected bool `yaml:"stayConnected,omitempty"`
	}
}

type Params struct {
	remoteIP   net.IP
	remotePort int
	bindMode   libsmpp.ConnSMPPMode
	submit     libsmpp.SMPPSubmit
}

type TrackProcessingTime struct {
	Count      uint
	DelayMin   time.Duration
	DelayMax   time.Duration
	DelayTotal time.Duration
	sync.RWMutex
}

type TLVDynamic struct {
	ID       libsmpp.TLVCode
	Template string
}
