package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const MaxStatsEntries = 180

type StatsEntry struct {
	UT        int64
	T         time.Time
	SentRate  uint32
	SentCount uint32
	SentRTD   uint32
	RecvRate  uint32
	RecvCount uint32
}

type StatCounter []struct {
	ID    string
	Value uint32
}

type StatsLog struct {
	List []StatsEntry
	mtx  sync.RWMutex
	ch   chan StatsEntry

	ctx context.Context
}

var statsLog StatsLog

//
//
func (s *StatsLog) Init(ctx context.Context) {
	s.ctx = ctx
	s.ch = make(chan StatsEntry)
	// s.List = make([]StatsEntry, MaxStatsEntries+1)

	go s.periodic()
}

// Create zero counters every 500 ms (if there're no data for this second)
func (s *StatsLog) periodic() {
	c := time.Tick(500 * time.Millisecond)
	for {
		select {
		case <-c:
			s.Update(time.Now(), StatCounter{})
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *StatsLog) update(e StatsEntry, list StatCounter) StatsEntry {
	for _, v := range list {
		switch v.ID {
		case "SentRate":
			e.SentRate += v.Value
		case "SentCount":
			e.SentCount += v.Value
		case "SentRTD":
			e.SentRTD += v.Value
		case "RecvRate":
			e.RecvRate += v.Value
		case "RecvCount":
			e.RecvCount += v.Value
		}
	}
	return e
}

func (s *StatsLog) Update(t time.Time, list StatCounter) {
	var skipCount int
	if len(s.List) >= MaxStatsEntries {
		skipCount = len(s.List) - MaxStatsEntries + 1
	}

	// Calculate UnixTime
	UT := t.Unix()

	// Lock Mutex
	s.mtx.RLock()

	// Insert new record
	if (len(s.List) == 0) || (s.List[len(s.List)-1].UT < UT) {
		now := time.Now()
		s.List = append(s.List[skipCount:], s.update(StatsEntry{UT: now.Unix(), T: now}, list))
		return
	}

	// Scan rows and append data in required position
	for i := len(s.List) - 1; i >= 0; i-- {
		// Update at current position
		if s.List[i].UT == UT {
			s.List[i] = s.update(s.List[i], list)
			return
		}
	}
	s.mtx.RUnlock()
}

func (s *StatsLog) reportStats() string {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if len(s.List) > 1 {
		// Strip last record, because it can be incomplete
		b, err := json.Marshal(s.List[:len(s.List)-1])
		if err != nil {
			return "{ \"error\": \"Failed to map JSON result\"} "
		}
		return string(b)
	} else {
		return "[]"
	}
}
