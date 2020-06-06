package main

import (
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/vponomarev/libsmpp"
	"net/http"
	"strconv"
)

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
				"type": "smpp-lb",
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
			h.config.Log.Rate = l
			log.WithFields(log.Fields{
				"type": "smpp-lb",
			}).Warning("Override LoggingRate to: ", l)
			fmt.Fprintf(w, "OK")
		} else {
			fmt.Fprintf(w, "ERROR:", err)
		}
	} else {
		fmt.Fprintln(w, h.config.Log.Rate)
	}
}
