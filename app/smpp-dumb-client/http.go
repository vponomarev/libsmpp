package main

import (
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
)

type HttpHandler struct {
	s      *libsmpp.SMPPSession
	config *Config
	sl     *StatsLog
}

type JTLV struct {
	ID          int32
	IsBase64    bool
	Value       string
	ValueBase64 []byte
}

type JSubmitRequest struct {
	From struct {
		TON  int    `json:"ton"`
		NPI  int    `json:"npi"`
		Addr string `json:"addr"`
	}
	To struct {
		TON  int    `json:"ton"`
		NPI  int    `json:"npi"`
		Addr string `json:"addr"`
	}
	RegisteredDelivery int    `json:"registeredDelivery"`
	DataCoding         int    `json:"dataCoding"`
	Body               string `json:"body"`
	TLV                []JTLV `json:"tlv"`
}

type JErrorResponse struct {
	Status    int
	ErrorCode int
	ErrorText string
}

func runProfiler(s *libsmpp.SMPPSession, config Config) {
	// Init profiler if enabled
	if config.Profiler.Enabled {
		if len(config.Profiler.Listen) == 0 {
			config.Profiler.Listen = "127.0.0.1:5800"
		}
		log.WithFields(log.Fields{"type": "smpp-client", "action": "profiler"}).Info("Starting profiler at: ", config.Profiler.Listen)

		hh := &HttpHandler{s: s, config: &config, sl: &statsLog}
		http.HandleFunc("/", hh.StatsRoot)
		http.HandleFunc("/getInfo", hh.StatsGetInfo)
		http.HandleFunc("/stat", hh.StatPage)

		go func(addr string) {
			err := http.ListenAndServe(addr, nil)
			if err != nil {
				log.WithFields(log.Fields{"type": "smpp-client", "action": "profiler"}).Fatal("ListenAndServe returned an error: ", err)
				return
			}
		}(config.Profiler.Listen)
	}
}

func (h *HttpHandler) StatsRoot(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fmt.Fprintln(w, "Error parsing request")
		return
	}

	fn := "html/index.html"
	t, err := template.ParseFiles(fn)
	if err != nil {
		fmt.Fprint(w, "Error parsing template file:", fn, " with error:", err)
		return
	}
	err = t.Execute(w, map[string]string{
		"ServerHost": r.Host,
		"Count":      fmt.Sprintf("%d", h.config.Generator.SendCount),
		"Rate":       fmt.Sprintf("%d", h.config.Generator.SendRate),
		"Window":     fmt.Sprintf("%d", h.config.Generator.SendWindow),
	})
	if err != nil {
		fmt.Fprint(w, "Internal server error: cannot process template")
	}
}

func (h *HttpHandler) StatsGetInfo(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fmt.Fprintln(w, "Error parsing request")
		return
	}

	w.Header().Add("Content-type", "application/json")
	fmt.Fprintln(w, h.sl.reportStats())
}

func (h *HttpHandler) StatPage(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fmt.Fprintln(w, "Error parsing request")
		return
	}

	file, err := os.Open("html/stat.html")
	if err != nil {
		fmt.Fprintln(w, "Cannot read file html/stat.html")
		return
	}
	data, err := ioutil.ReadAll(file)
	if err != nil {
		fmt.Fprintln(w, "IO Read error: ", err)
		return
	}
	fmt.Fprintln(w, strings.ReplaceAll(string(data), "{HostIP}", h.config.Profiler.Listen))
}

// ======================================================================== //
// HTTP Request processor                                                   //
// ======================================================================== //

func runListener(s *libsmpp.SMPPSession, config Config) {
	// Init profiler if enabled
	if config.HTTP.Incoming.Enabled {
		if len(config.HTTP.Incoming.Listen) == 0 {
			config.HTTP.Incoming.Listen = "127.0.0.1:5801"
		}
		log.WithFields(log.Fields{"type": "smpp-client", "action": "HTTP-Incoming"}).Info("Starting HTTP Listener at: ", config.HTTP.Incoming.Listen, config.HTTP.Incoming.URL)

		hh := &HttpHandler{s: s, config: &config, sl: &statsLog}

		mux := http.NewServeMux()
		mux.HandleFunc(config.HTTP.Incoming.URL, hh.ProcessIncomingRequest)

		err := http.ListenAndServe(config.HTTP.Incoming.Listen, mux)
		if err != nil {
			log.WithFields(log.Fields{"type": "smpp-client", "action": "HTTP-Incoming"}).Fatal("ListenAndServe returned an error: ", err)
			return
		}
	}
}

func (h *HttpHandler) ProcessIncomingRequest(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fmt.Fprintln(w, "Error parsing request")
		return
	}

	if h.config.DebugFeatures.HTTPIncoming {
		line := "{\"From\":{\"ton\":5, \"npi\": 0, \"addr\": \"79031234567\"}, \"TLV\":[{\"ID\": 12345, \"Value\":\"Message text\", \"ValueBase64\":\"VGVzdCBtZXNzYWdlCg==\"}]}"
		var jr JSubmitRequest

		if err := json.Unmarshal([]byte(line), &jr); err != nil {
			er := JErrorResponse{
				Status:    0,
				ErrorCode: 0,
				ErrorText: err.Error(),
			}
			if es, err := json.Marshal(er); err != nil {
				fmt.Fprint(w, "Error generating JSON Result: ", err)
			} else {
				fmt.Fprint(w, string(es))
			}
			return
		}

		fmt.Fprint(w, jr)
	}
}
