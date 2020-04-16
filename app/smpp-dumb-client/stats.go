package main

import (
	"encoding/json"
	"sync"
	"time"
)

const MaxStatsEntries = 30

type StatsEntry struct {
	UT        int64
	T         time.Time
	SentRate  uint
	SentCount uint
	SentRTD   uint
	RecvRate  uint
	RecvCount uint
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

func postUpdateStats(se StatsEntry) {
	var offset int
	if len(statsLog.List) >= MaxStatsEntries {
		offset = 1
	}
	// Save UnixTime
	se.UT = se.T.Unix()

	// Lock MTX
	statsLog.mtx.RLock()

	// Check last record
	if len(statsLog.List) < 1 {
		statsLog.List = append(statsLog.List[offset:], se)
		return
	}

	l := statsLog.List[len(statsLog.List)-1]
	if l.UT == se.UT {
		// UPDATE Call
		if se.SentRate > 0 {
			l.SentRate = se.SentRate
		}
		if se.SentRTD > 0 {
			l.SentRTD = se.SentRTD
		}
		if se.SentCount > 0 {
			l.SentCount = se.SentCount
		}
		if se.RecvRate > 0 {
			l.RecvRate = se.RecvRate
		}
		if se.RecvCount > 0 {
			l.RecvCount = se.RecvCount
		}

		statsLog.List[len(statsLog.List)-1] = l
	} else {
		statsLog.List = append(statsLog.List[offset:], se)
	}

	// Unlock MTX
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
