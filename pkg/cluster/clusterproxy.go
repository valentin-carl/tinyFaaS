package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/OpenFogStack/tinyFaaS/pkg/rproxy"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"
)

func Call(r *http.Request, timeout int, async bool, registeredFunctions map[string][]string) (rproxy.Status, []byte) {

	// todo check somewhere if called function actually exists => as in rproxy.Call

	// set up stuff for sending the request
	client := http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	// get nodes
	nodes := getShuffledNodes() // TODO this might be nil
	log.Printf("clusterproxy got %d nodes to try", len(nodes))

	handle := func() (rproxy.Status, []byte) {
		for range nodes {

			// pick one at random
			node := nodes[0]
			log.Printf("picked node %s", node.String())

			// send request
			url := fmt.Sprintf(`http://%s:%d%s`, node.Ip, node.RproxyPort, r.URL.Path)
			log.Printf("trying to call node with request url %s", url)
			tmp, err := io.ReadAll(r.Body)
			if err != nil {
				panic(err)
			}
			log.Println(len(tmp))
			res, err := forwardRequest(r, url, &client, tmp)

			if err != nil {
				// request didn't go through
				log.Printf("error sending request %s, trying different node if one is available", err.Error())
			} else {
				log.Println(res.StatusCode) // this used to cause a null pointer exception, was before the if statement
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

	// problem: the rproxy and manager are seperate programs
	// hence, they don't access the same nodes slices
	// solution: if this function is called by the rproxy,
	// send request to manager/listnodes and get the data from there

	// this assumes that the manager and rproxy are running on the same host
	// get all currently registered nodes from the local manager
	mport := os.Getenv("CONFIG_PORT")
	if mport == "" {
		log.Println("could not get managerport, defaulting to 8080")
		mport = "8080"
	}
	url := fmt.Sprintf("http://localhost:%s/cluster/list", mport)
	res, err := http.Get(url)
	if err != nil {
		log.Println("error while getting nodes from manager", err.Error())
		return nil
	}
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println("error while trying to read response body")
		return nil
	}
	var nodes []Node
	err = json.Unmarshal(resBody, &nodes)
	if err != nil {
		log.Println("error while trying to unmarshall reponse body")
		return nil
	}
	log.Printf(`"getShuffledNodes" got %d nodes %p`, len(nodes), &nodes)

	// suffle nodes and return
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})
	return nodes
}

func forwardRequest(r *http.Request, url string, client *http.Client, body []byte) (*http.Response, error) {

	// this function is required because the request object the server receives cannot be sent again
	// solution: create a manual copy and send that to the target

	log.Printf(`forwarding request with body %s to %s`, string(body), url)

	// create new request
	req, err := http.NewRequest(r.Method, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("error creating new request")
		return nil, err
	}

	// send request to forwarding target and return result
	return client.Do(req)
}
