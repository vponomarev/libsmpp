package main

import (
	"encoding/json"
	"sync"
	"time"
)

const MaxStatsEntries = 30

type StatsEntry struct {
	T         time.Time
	Rate      uint
	SentCount uint
}

type StatsLog struct {
	List []StatsEntry
	mtx  sync.RWMutex
}

var statsChan chan StatsEntry
var statsLog StatsLog

// Init statistics channel
func initStats() {
	statsChan = make(chan StatsEntry)
	statsLog.List = make([]StatsEntry, MaxStatsEntries+1)
}

func postStats(se StatsEntry) {
	var offset int
	if len(statsLog.List) >= MaxStatsEntries {
		offset = 1
	}
	statsLog.mtx.RLock()
	statsLog.List = append(statsLog.List[offset:], se)
	statsLog.mtx.RUnlock()
}

func reportStats() string {
	statsLog.mtx.RLock()
	defer statsLog.mtx.RUnlock()
	b, err := json.Marshal(statsLog.List)
	if err != nil {
		return "{ \"error\": \"Failed to map JSON result\"} "
	}
	return string(b)
}
