package main

import (
	"log"
	"os"
	"sync"
	"time"
)

type AppState struct {
	mu         sync.Mutex
	connected  bool
	logging    bool
	lastBPM    int
	hasBPM     bool
	staleTimer *time.Timer
}

func (s *AppState) snapshot() (connected, logging bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected, s.logging
}

func (s *AppState) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

func (s *AppState) setLogging(v bool) {
	s.mu.Lock()
	s.logging = v
	s.mu.Unlock()
}

func (s *AppState) resetBPM() {
	s.mu.Lock()
	s.hasBPM = false
	s.mu.Unlock()
}

func (s *AppState) onConnect() {
	s.mu.Lock()
	s.connected = true
	if s.staleTimer != nil {
		s.staleTimer.Stop()
		s.staleTimer = nil
	}
	s.mu.Unlock()
}

func (s *AppState) onDisconnect() {
	s.mu.Lock()
	s.connected = false
	if s.staleTimer != nil {
		s.staleTimer.Stop()
	}
	s.staleTimer = time.AfterFunc(staleBPMTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.connected {
			return
		}
		s.hasBPM = false
		if err := os.WriteFile(outputPath(), []byte{}, 0644); err != nil {
			log.Printf("[BPM] could not clear output: %v", err)
		}
	})
	s.mu.Unlock()
}

func (s *AppState) onSwitch() {
	s.mu.Lock()
	s.connected = false
	s.hasBPM = false
	if s.staleTimer != nil {
		s.staleTimer.Stop()
		s.staleTimer = nil
	}
	s.mu.Unlock()
	os.WriteFile(outputPath(), []byte{}, 0644)
}
