// Command echoserver is a tiny HTTP target for local Horse v2 tests.
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	address := "127.0.0.1:28080"
	if len(os.Args) > 1 {
		address = os.Args[1]
	}
	http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, "horse2-ok")
	})
	if err := http.ListenAndServe(address, nil); err != nil {
		fmt.Fprintln(os.Stderr, "echoserver:", err)
		os.Exit(1)
	}
}
