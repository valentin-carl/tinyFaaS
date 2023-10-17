package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/OpenFogStack/tinyFaaS/pkg/manager"
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
	nodes        []Node            // keep a slice of known (meaning known to the handler) nodes so the refresh only deploys at nodes that don't have this function already
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

func (cb *ClusterBackend) Create(name string, env string, threads int, dirPath string, envs map[string]string) (manager.Handler, error) {

	log.Printf("creating cluster function handler for %s\n", name)

	zip64, err := manager.GetFunction(name)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Unable to read encoded function code for %s", name))
	}

	// create & return function handler
	fh := &clusterHandler{
		functionName: name,
		environment:  env,
		nThreads:     threads,
		nodes:        make([]Node, 0),
		functionCode: zip64,
		envs:         envs,
	}

	log.Println("created function handler")

	return fh, nil
}

func (cb *ClusterBackend) Stop() error {
	return nil // TODO is there a way to remotely tell the nodes to shut down? If not, build one
}

func (ch *clusterHandler) IPs() []string {
	return stream.
		Map(stream.OfSlice(ch.nodes),
			func(n Node) string {
				return n.Ip
			}).
		ToSlice()
}

// Start checks for new nodes and sends all currently registered nodes the function
func (ch *clusterHandler) Start() error {

	// in case new nodes were added between creating this handler and starting it
	ch.refresh()

	// send function to tinyfaas nodes
	for _, n := range ch.nodes {
		err := ch.uploadToNode(n)
		if err != nil {
			log.Printf("Error occurred while trying to upload function '%s' to node %s: %s", ch.functionName, n.Ip, err.Error())
			return err // TODO is it better to return immediately or try the other nodes too?
		}
	}

	return nil
}

func (ch *clusterHandler) Destroy() error {
	// send request to all nodes to destroy function with name = ch.functionName
	return nil
}

// Query all nodes for logs for this handler's function an return them
func (ch *clusterHandler) Logs() (io.Reader, error) {

	var logs bytes.Buffer

	for _, ip := range ch.nodes {
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
			if match, _ := regexp.MatchString(fmt.Sprintf("function=%s", ch.functionName), line); match {
				logs.WriteString(fmt.Sprintf("%s\n", line))
			}
		}
	}

	return &logs, nil
}

// refresh is used when this function handler is already running (i.e. Create & Start were already called).
// It compares nodes stored in `nodes` to the nodes stored in the cluster registry.
// The function is then sent to the new nodes.
func (ch *clusterHandler) refresh() []Node {

	var new []Node

	// find new nodes
	nodes := GetNodes()
Outer:
	for _, oldNode := range ch.nodes {
		for _, node := range nodes {
			if oldNode.Ip == node.Ip {
				continue Outer
			}
		}
		new = append(new, oldNode)
	}

	// if there are any, append to ch.nodes
	if len(new) != 0 {
		ch.nodes = append(ch.nodes, new...)
	}

	return new
}

func (ch *clusterHandler) RefreshAndUpload() {

	deployTo := ch.refresh()

	// if there are no new nodes, just stop here
	if len(deployTo) == 0 {
		log.Printf("no new nodes to deploy function %s to\n", ch.functionName)
		return
	}

	// send the function to all new nodes
	for _, ep := range deployTo {
		err := ch.uploadToNode(ep)
		if err != nil {
			log.Printf("error while trying to upload function to node %s: %s\n", ep.Ip, err.Error())
		}
	}
}

func (ch *clusterHandler) uploadToNode(node Node) error {

	// create the function body
	var body []byte
	zip := map[string]any{
		"name":    ch.functionName,
		"env":     ch.environment,
		"threads": ch.nThreads,
		"zip":     ch.functionCode,
		"envs":    ch.envs,
	}
	body, err := json.Marshal(zip)
	if err != nil {
		return err
	}

	// create the request
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s:%d/upload", node.Ip, node.ManagerPort), bytes.NewReader(body))
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
	log.Printf("node %s response: %s\n", node.Ip, res.Header)

	return nil
}

// TODO add call function here => probably best as a method for handler
