package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	httpPprof "net/http/pprof"
	"os"
	"runtime/pprof"
	"time"
)

func startRootProfile(enabled bool) (func(), error) {
	if !enabled {
		return func() {}, nil
	}
	profileFile, err := os.Create("hercules.pprof")
	if err != nil {
		return nil, fmt.Errorf("create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(profileFile); err != nil {
		_ = profileFile.Close()
		return nil, fmt.Errorf("start CPU profile: %w", err)
	}

	server := &http.Server{
		Addr:              "localhost:6060",
		Handler:           profilingHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("profiling server: %v", err)
		}
	}()
	return func() {
		pprof.StopCPUProfile()
		if err := profileFile.Close(); err != nil {
			log.Printf("close CPU profile: %v", err)
		}
		if err := server.Close(); err != nil {
			log.Printf("close profiling server: %v", err)
		}
	}, nil
}

func profilingHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", httpPprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httpPprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httpPprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httpPprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httpPprof.Trace)

	return mux
}
