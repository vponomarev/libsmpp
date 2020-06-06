package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
)

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
