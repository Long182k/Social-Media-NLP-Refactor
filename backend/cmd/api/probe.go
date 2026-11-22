package main

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func probeReady() bool {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return false
	}
	client := http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/readyz") // #nosec G704 -- Fixed loopback destination with a validated numeric port and redirects disabled.
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}
