package http

import (
	"github.com/OpenFogStack/tinyFaaS/pkg/cluster"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/OpenFogStack/tinyFaaS/pkg/rproxy"
)

func Start(r *rproxy.RProxy, listenAddr string) {

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		p := req.URL.Path

		for p != "" && p[0] == '/' {
			p = p[1:]
		}

		async := req.Header.Get("X-tinyFaaS-Async") != ""

		log.Printf("have request for path: %s (async: %v)", p, async)

		req_body, err := io.ReadAll(req.Body)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err)
			return
		}

		// TODO this is the place to call the "clusterCall" function
		backend, ok := os.LookupEnv("TF_BACKEND")
		var (
			s   rproxy.Status
			res []byte
		)
		if ok && backend == "cluster" {
			// use clusterproxy to forward calls to other nodes
			s, res = cluster.Call(req, 5, async, r.Hosts)
		} else {
			// use normal rproxy to execute calls locally
			s, res = r.Call(p, req_body, async)
		}

		switch s {
		case rproxy.StatusOK:
			w.WriteHeader(http.StatusOK)
			w.Write(res)
		case rproxy.StatusAccepted:
			w.WriteHeader(http.StatusAccepted)
		case rproxy.StatusNotFound:
			w.WriteHeader(http.StatusNotFound)
		case rproxy.StatusError:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	log.Printf("Starting HTTP server on %s", listenAddr)
	err := http.ListenAndServe(listenAddr, mux)

	if err != nil {
		log.Fatal(err)
	}

	log.Print("HTTP server stopped")

}
