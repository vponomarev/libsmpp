package main

import (
	"fmt"
	"github.com/vponomarev/libsmpp"
	"net/http"
)

type HttpHandler struct {
	s      *libsmpp.SMPPSession
	config *Config
}

func (h *HttpHandler) StatsGetInfo(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	w.Header().Add("Content-type", "application/json")
	fmt.Fprintln(w, reportStats())
}
