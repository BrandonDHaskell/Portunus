package httpapi

import (
	"log"
	"net/http"
	"time"
)

func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now().UTC()
		next.ServeHTTP(w, r)
		logger.Printf("%s %s from=%s dur=%s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}
