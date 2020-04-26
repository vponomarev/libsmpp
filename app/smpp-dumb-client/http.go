package main

import (
	"fmt"
	"github.com/vponomarev/libsmpp"
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

func (h *HttpHandler) StatsGetInfo(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	w.Header().Add("Content-type", "application/json")
	fmt.Fprintln(w, h.sl.reportStats())
}

func (h *HttpHandler) StatPage(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

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
