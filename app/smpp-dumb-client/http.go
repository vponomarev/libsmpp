package main

import (
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

func runProfiler(s *libsmpp.SMPPSession, config Config) {
	// Init profiler if enabled
	if config.Profiler {
		if len(config.ProfilerListen) == 0 {
			config.ProfilerListen = "127.0.0.1:5800"
		}
		log.WithFields(log.Fields{"type": "smpp-client", "action": "profiler"}).Info("Starting profiler at: ", config.ProfilerListen)

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
		}(config.ProfilerListen)
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
	t.Execute(w, map[string]string{
		"ServerHost": r.Host,
		"Count":      fmt.Sprintf("%d", h.config.SendCount),
		"Rate":       fmt.Sprintf("%d", h.config.SendRate),
		"Window":     fmt.Sprintf("%d", h.config.SendWindow),
	})
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
	fmt.Fprintln(w, strings.ReplaceAll(string(data), "{HostIP}", h.config.ProfilerListen))
}
