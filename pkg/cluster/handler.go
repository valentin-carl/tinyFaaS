package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/OpenFogStack/tinyFaaS/pkg/manager"
	"github.com/OpenFogStack/tinyFaaS/pkg/util"
	"github.com/mariomac/gostream/stream"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// handles instances of a single function across multiple TF nodes
type clusterHandler struct {
	functionName string
	environment  string
	nThreads     int
	endpoints    []Node
	functionCode string            // TODO add function code here? is there a better way?
	envs         map[string]string // forward environment variables to nodes for docker containers (?)
}

type ClusterBackend struct {
	tinyFaaSID string
}

func New(tinyFaaSID string) *ClusterBackend {
	return &ClusterBackend{
		tinyFaaSID: tinyFaaSID,
	}
}

func (cb *ClusterBackend) Create(name string, env string, threads int, filedir string, envs map[string]string) (manager.Handler, error) {

	// prepare function to be sent => zip etc.
	// for all node ips
	//	send POST to /upload

	// store the function here *somehow* to later send it to the nodes
	// needs to be in the same format that upload.sh creates because the
	// request will be similar
	// in dh.Start()

	functionSource, err := util.EncodeDir(filedir)
	if err != nil {
		log.Println("error while trying to encode the function code:", err.Error())
		return nil, err
	}

	return nil, nil
}

func (cb *ClusterBackend) Stop() error {
	return nil // TODO is there a way to remotely tell the nodes to shut down? If not, build one
}

func (dh *clusterHandler) IPs() []string {
	return stream.
		Map(stream.OfSlice(dh.endpoints),
			func(n Node) string {
				return n.ip
			}).
		ToSlice()
}

func (dh *clusterHandler) Start() error {
	// todo send the function to all nodes => see upload.sh for how to send the code via http
	// for that to work, the type probably also needs all the info about the function
	return nil
}

func (dh *clusterHandler) Destroy() error {
	// send request to all nodes to destroy function with name = dh.functionName
	return nil
}

// Query all nodes for logs for this handler's function an return them
func (dh *clusterHandler) Logs() (io.Reader, error) {

	var logs bytes.Buffer

	for _, ip := range dh.endpoints {
		// request logs from node
		// TODO this assumes that all tinyfaas nodes have the same ports config, which doesn't need to be true
		managementPort := os.Getenv("CONFIG_PORT")
		if managementPort == "" {
			return nil, errors.New("Could not get the management service's port.")
		}
		res, err := http.Get(fmt.Sprintf("http://%s:%d/logs", ip, managementPort))
		if err != nil {
			return nil, err
		}

		// read response into string slice
		resContents, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}
		bodyStr := string(resContents)
		bodyStr = strings.TrimSpace(bodyStr)
		lines := strings.Split(bodyStr, "\n")

		// filter lines by function name
		for _, line := range lines {
			if match, _ := regexp.MatchString(fmt.Sprintf("function=%s", dh.functionName), line); match {
				logs.WriteString(fmt.Sprintf("%s\n", line))
			}
		}
	}

	return &logs, nil
}

// Refresh is used when this function handler is already running (i.e. Create & Start were already called).
// It compares endpoints stored in `endpoints` to the endpoints stored in the cluster registry.
// The function is then sent to the new nodes.
func (dh *clusterHandler) Refresh() {

	var deployTo []Node

	// find new nodes
	nodes := GetNodes()
Outer:
	for _, oldNode := range dh.endpoints {
		for _, node := range nodes {
			if oldNode.ip == node.ip {
				continue Outer
			}
		}
		deployTo = append(deployTo, oldNode)
	}

	// send post requests to all new nodes /upload
	if len(deployTo) == 0 {
		log.Printf("no new nodes to deploy function %s to\n", dh.functionName)
	}
	for _, ep := range deployTo {
		err := dh.uploadToNode(ep)
		if err != nil {
			log.Printf("error while trying to upload function to node %s: %s\n", ep.ip, err.Error())
		}
	}
}

func (dh *clusterHandler) uploadToNode(node Node) error {

	// create the function body
	var body []byte
	zip := map[string]any{
		"name":    dh.functionName,
		"env":     dh.environment,
		"threads": dh.nThreads,
		"zip":     dh.functionCode,
		"envs":    dh.envs,
	}
	body, err := json.Marshal(zip)
	if err != nil {
		return err
	}

	// create the request
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s:%d/upload", node.ip, node.managerPort), bytes.NewReader(body))
	log.Println("sending request:", req.Header, req.Body)
	if err != nil {
		return err
	}

	// send it and log the response
	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	log.Printf("node %s response: %s\n", node.ip, res.Header)

	return nil
}

// TODO add call function here => probably best as a method for handler
