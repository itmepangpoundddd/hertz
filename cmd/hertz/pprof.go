package main

import (
	"net/http"
	"net/http/pprof"
)

// registerPProf exposes the standard Go profiler on the metrics listener.
// Enable it only behind a private interface or a reverse-proxy allowlist.
func registerPProf(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}
