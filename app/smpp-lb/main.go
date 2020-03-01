package main

import (
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"libsmpp"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	//	"sync"
	"time"
)

// FLAG: Stop handling traffic
var stopCh chan struct{}

type Config struct {
	Server struct {
		Port int
	}
	LogLevel       string `yaml:"logLevel,omitempty"`
	Profiler       bool   `yaml:"profiler,omitempty"`
	ProfilerListen string `yaml:"profilerListen,omitempty"`
	Logging        struct {
		Server struct {
			Rate bool
		}
	}
	Client struct {
		Remote string
	}

	DebugNetBuf bool `yaml:"debugNetBuf"`
}

type Params struct {
	LogLevel log.Level
	Flags    struct {
		LogLevel bool
	}
}

func doStop() bool {
	select {
	case <-stopCh:
		return true
	default:
		close(stopCh)
	}
	return false
}

type HttpHandler struct {
	p      *libsmpp.SessionPool
	config *Config
}

// [ /session/list ]
func (h *HttpHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	sl := h.p.GetSessionList()
	/*
		for k, v := range sl {
			fmt.Fprintln(w, k, ":", v, v.Cs.Error(), v.Cs.NError(), v.Cs.GetDirection())
		}
	*/

	jv, err := json.Marshal(sl)
	if err != nil {
		fmt.Fprintln(w, ": Error generating JSON")
		return
	}
	w.Header().Add("Content-type", "application/json")
	fmt.Fprintln(w, string(jv))
}

// [ /session/stat ]
func (h *HttpHandler) SessionStats(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

}

// [ /log/level ]
func (h *HttpHandler) HttpLogLevel(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	level := r.FormValue("level")
	if len(level) > 0 {
		if l, err := log.ParseLevel(level); err == nil {
			log.SetLevel(l)
			log.WithFields(log.Fields{
				"type": "smpp-client",
			}).Warning("Override LogLevel to: ", l.String())
			fmt.Fprintf(w, "OK")
		} else {
			fmt.Fprintf(w, "ERROR:", err)
		}
	} else {
		fmt.Fprintf(w, log.GetLevel().String())
	}
}

// [ /log/rate ]
func (h *HttpHandler) HttpLogRate(w http.ResponseWriter, r *http.Request) {
	rate := r.FormValue("rate")
	if len(rate) > 0 {
		if l, err := strconv.ParseBool(rate); err == nil {
			h.config.Logging.Server.Rate = l
			log.WithFields(log.Fields{
				"type": "smpp-lb",
			}).Warning("Override LoggingRate to: ", l)
			fmt.Fprintf(w, "OK")
		} else {
			fmt.Fprintf(w, "ERROR:", err)
		}
	} else {
		fmt.Fprintln(w, h.config.Logging.Server.Rate)
	}
}

func ProcessCMDLine() (p Params) {
	// Set default
	p.LogLevel = log.InfoLevel

	var pv bool
	var pvn string
	for _, param := range os.Args[1:] {
		if pv {
			switch pvn {
			// LogLevel
			case "-log":
				l, err := log.ParseLevel(param)
				if err != nil {
					l = log.InfoLevel
					fmt.Print("Incorrect LogLevel [", param, "], set LogLevel to: ", l.String())
				} else {
					p.LogLevel = l
					p.Flags.LogLevel = true
				}
			}
		} else {
			switch param {
			case "-log":
				pvn = param
				pv = true
			}
		}
	}
	return p
}

func hConn(id uint32, conn *net.TCPConn, pool *libsmpp.SessionPool, config *Config) {

	// Allocate new SMPP Session structure
	s := &libsmpp.SMPPSession{
		ManualBindValidate: true,
		DebugLevel:         1,
		SessionID:          id,
		DebugNetBuf:        config.DebugNetBuf,
	}
	s.Init()

	go s.RunIncoming(conn, id)

	go func(p *libsmpp.SessionPool, config *Config, s *libsmpp.SMPPSession) {
		var sv uint32
		sv = 0
		c := time.Tick(1000 * time.Millisecond)
		for {

			select {
			case <-c:
				if config.Logging.Server.Rate {
					sn := p.GetLastTransactionID()
					if sn > sv {
						fmt.Println("[", s.SessionID, "] During last 1s: ", sn-sv)
						sv = sn
					} else {
						fmt.Println("[", s.SessionID, "] During last 1s: -")
					}
				}
			case <-s.Closed:
				return
			}
		}
	}(pool, config, s)

	for {
		select {
		// Request for BIND validation
		case x := <-s.BindValidator:
			r := libsmpp.BindValidatorResponce{
				ID:     x.ID,
				SMSCID: "GoLib32",
				Status: 0,
			}
			s.BindValidatorR <- r

		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "smpp-lb", "SID": s.SessionID, "service": "inConnect", "action": "StatusUpdate"}).Warning(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {
				// Pass session to SessionPool
				pool.RegisterSession(s)
				fmt.Println("# pool.RegisterSession() execution finished, stopping hConn() processing")
				return
			}

		case <-s.Closed:
			fmt.Println("[", id, "] Connection is closed!")
			return
		}
	}

}

func main() {
	pParam := ProcessCMDLine()

	fmt.Println("LogLevel:", pParam.LogLevel.String())

	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	log.WithFields(log.Fields{
		"type": "smpp-lb",
	}).Info("Start")

	// Load configuration file
	config := Config{}

	configFileName := "config.yml"
	source, err := ioutil.ReadFile(configFileName)
	if err == nil {
		if err = yaml.Unmarshal(source, &config); err == nil {
			log.WithFields(log.Fields{
				"type": "smpp-lb",
			}).Info("Loaded configuration file: ", configFileName)
		} else {
			fmt.Println("Error loading config file: ", err)
			return
		}
	}

	// Load LogLevel from config if present
	if (len(config.LogLevel) > 0) && (!pParam.Flags.LogLevel) {
		if l, err := log.ParseLevel(config.LogLevel); err == nil {
			pParam.LogLevel = l

			log.SetLevel(pParam.LogLevel)
			log.WithFields(log.Fields{
				"type": "smpp-client",
			}).Warning("Override LogLevel to: ", pParam.LogLevel.String())
		}
	}

	// Fill default values for config
	if config.Server.Port < 1 {
		config.Server.Port = 2775
	}

	// Split REMOTE HOST:PORT
	remote := strings.Split(config.Client.Remote, ":")
	if len(remote) != 2 {
		log.WithFields(log.Fields{"type": "smpp-lb"}).Fatal("Cannot parse remote ip:port (", config.Client.Remote, ")")
		return
	}

	remoteIP := net.ParseIP(remote[0])
	if remoteIP == nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Invalid destination IP:", remote[0])
		return
	}

	remotePort, err := strconv.ParseUint(remote[1], 10, 16)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-client"}).Fatal("Invalid destination Port:", remote[1])
		return
	}

	pool := libsmpp.SessionPool{}
	pool.Init()

	// Init profiler if enabled
	if config.Profiler {
		if len(config.ProfilerListen) == 0 {
			config.ProfilerListen = "127.0.0.1:5801"
		}
		log.WithFields(log.Fields{"type": "smpp-lb", "action": "profiler"}).Info("Starting profiler at: ", config.ProfilerListen)

		hh := &HttpHandler{p: &pool, config: &config}

		http.HandleFunc("/session/list", hh.ListSessions)
		http.HandleFunc("/log/level", hh.HttpLogLevel)
		http.HandleFunc("/log/rate", hh.HttpLogRate)

		go func(addr string) {
			err := http.ListenAndServe(addr, nil)
			if err != nil {
				log.WithFields(log.Fields{"type": "smpp-lb", "action": "profiler"}).Fatal("ListenAndServe returned an error: ", err)
				return
			}
		}(config.ProfilerListen)
	}

	var id uint32 = 1

	dest := &net.TCPAddr{IP: remoteIP, Port: int(remotePort)}
	go outConnect(id, dest, &pool, &config)

	// Listen socket for new connections
	lAddr := &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: config.Server.Port}

	socket, err := net.ListenTCP("tcp4", lAddr)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-lb"}).Error("Error listening TCP socket: ", err)
		return
	}
	log.WithFields(log.Fields{"type": "smpp-lb", "service": "ListenTCP"}).Warning("Starting listening on: ", socket.Addr().String())

	for {
		conn, err := socket.AcceptTCP()
		id++
		if err != nil {
			log.WithFields(log.Fields{"type": "smpp-lb"}).Error("Error accepting socket connection: ", err)
			return
		}
		log.WithFields(log.Fields{"type": "smpp-lb", "remoteIP": conn.RemoteAddr().String()}).Warning("Received incoming conneciton")
		go hConn(id, conn, &pool, &config)
	}
}

func outConnect(id uint32, dest *net.TCPAddr, pool *libsmpp.SessionPool, config *Config) {
	conn, err := net.DialTCP("tcp", nil, dest)
	if err != nil {
		log.WithFields(log.Fields{"type": "smpp-lb", "service": "outConnect", "remote": dest}).Warning("Cannot connect to")
		return
	}
	log.WithFields(log.Fields{"type": "smpp-lb", "service": "outConnect", "remote": dest}).Info("TCP Connection established")

	s := &libsmpp.SMPPSession{
		SessionID:   id,
		DebugNetBuf: config.DebugNetBuf,
	}
	s.Init()

	go s.RunOutgoing(conn, libsmpp.SMPPBind{
		ConnMode:   libsmpp.CSMPPTRX,
		SystemID:   "test_dp",
		Password:   "test12",
		SystemType: "",
		IVersion:   0,
		AddrTON:    0,
		AddrNPI:    0,
		AddrRange:  "",
	},
		id)

forLoop:
	for {
		select {
		case x := <-s.Status:
			log.WithFields(log.Fields{"type": "smpp-lb", "SID": s.SessionID, "service": "outConnect", "action": "StatusUpdate"}).Warning(x.GetDirection().String(), ",", x.GetTCPState().String(), ",", x.GetSMPPState().String(), ",", x.GetSMPPMode().String(), ",", x.Error(), ",", x.NError())
			if x.GetSMPPState() == libsmpp.CSMPPBound {
				// Pass session to SessionPool
				go pool.RegisterSession(s)
				break forLoop
			}

		case <-s.Closed:
			log.WithFields(log.Fields{"type": "smpp-lb", "SID": s.SessionID, "service": "outConnect", "action": "close"}).Warning("Connection is closed")
			return
		}
	}

	// Session Close event fired
	<-s.Closed

	log.WithFields(log.Fields{"type": "smpp-lb", "service": "outConnect", "remote": dest}).Info("Session is closed")
	return
}
