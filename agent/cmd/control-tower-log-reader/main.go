// A local application/Nginx log-only broker. Agent connects over a Unix socket.
package main

import (
	"context"
	"controltower/agent/internal/containerlogs"
	cl "controltower/internal/containerlog"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	socket := flag.String("socket", "/run/control-tower-log-reader/reader.sock", "Unix socket path")
	containers := flag.String("containers", os.Getenv("CT_LOG_CONTAINERS"), "optional new-api container names; Nginx sources are discovered separately")
	timezone := flag.String("timezone", os.Getenv("CT_LOG_TIMEZONE"), "timezone for timestamps without offset (default Asia/Shanghai)")
	flag.Parse()
	if *timezone == "" {
		*timezone = "Asia/Shanghai"
	}
	names := []string{}
	for _, n := range strings.Split(*containers, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !cl.ValidName(n) {
			log.Fatal("configure valid allowed containers")
		}
		names = append(names, n)
	}
	if len(names) > 100 {
		log.Fatal("too many containers")
	}
	// systemd RuntimeDirectory owns the socket directory; remove only an old socket.
	if st, err := os.Lstat(*socket); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			log.Fatal("socket path is not a socket")
		}
		if err = os.Remove(*socket); err != nil {
			log.Fatal(err)
		}
	}
	ln, err := net.Listen("unix", *socket)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	if err = os.Chmod(*socket, 0660); err != nil {
		log.Fatal(err)
	}
	reader, err := containerlogs.NewReaderWithTimezone(names, *timezone)
	if err != nil {
		log.Fatal("invalid log timezone")
	}
	reader.Refresh(context.Background(), true)
	server := &http.Server{Handler: reader, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	log.Fatal(server.Serve(ln))
}
