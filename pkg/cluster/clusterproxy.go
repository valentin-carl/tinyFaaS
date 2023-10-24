package cluster

import (
	"context"
	"github.com/OpenFogStack/tinyFaaS/pkg/rproxy"
	"io"
	"log"
	"math/rand"
	"net/http"
	"time"
)

func Call(r *http.Request, timeout int, async bool, registeredFunctions map[string][]string) (rproxy.Status, []byte) {

	// todo check somewhere if called function actually exists => as in rproxy.Call

	// set up stuff for sending the request
	client := http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}
	req := r.Clone(context.Background())
	defer req.Body.Close()

	// get nodes
	nodes := getShuffledNodes()
	log.Printf("clusterproxy got %d nodes to try", len(nodes))

	handle := func() (rproxy.Status, []byte) {
		for range nodes {

			// pick one at random
			node := nodes[0]
			log.Printf("picked node %s", node.String())

			// send request
			res, err := client.Do(req)
			if err != nil {
				// request didn't go through
				log.Printf("error sending request %s, trying different node if one is available", err.Error())
			} else {
				// request went through
				// read response
				resBody, err := io.ReadAll(res.Body)
				log.Printf("%d got response %s from %s", res.StatusCode, string(resBody), node.String())
				if err != nil {
					log.Printf("could not read response body %s", string(resBody))
				}

				switch res.StatusCode {
				case 200:
					{
						log.Printf("request successful")
						return rproxy.StatusOK, resBody
					}
				case 202:
					{
						log.Printf("request accepted")
						return rproxy.StatusAccepted, resBody
					}
				default:
					{
						log.Printf("request reached node but error was returned")
					}
					// if reached, the request reached a node but returned an error => forward error to client
					// reason: function might have had side effect that should not happen twice(?)
					return rproxy.StatusError, nil
				}
			}

			// try next node
			log.Printf("could not send request to %s, trying different nodes, %s", node.String(), err.Error())
			nodes = nodes[1:]
		}

		// if reached, there was no node that could be called successfully
		log.Printf("there was no node that could be successfully called for request on function %s", r.URL.Path)
		return rproxy.StatusError, nil
	}

	// perform the request
	if async {
		log.Println("doing async request")
		go handle()
		return rproxy.StatusAccepted, nil
	} else {
		log.Println("doing sync request")
		return handle()
	}
}

func getShuffledNodes() []Node {
	nodes := GetNodes()
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})
	return nodes
}
