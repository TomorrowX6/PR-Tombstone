package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	target := "http://127.0.0.1:8080/readyz"
	if len(os.Args) > 1 {
		target = os.Args[1]
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fmt.Fprintln(os.Stderr, response.Status)
		os.Exit(1)
	}
}
