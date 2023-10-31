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

	log.Printf("cluster fh started for function %s\n", ch.functionName)

	// in case new nodes were added between creating this handler and starting it
	ch.refresh()

	log.Printf("fh %s knows about %d nodes\n", ch.functionName, len(ch.nodes))

	// send function to tinyfaas nodes
	for _, n := range ch.nodes {

		log.Printf("Uploading %s to %s\n", ch.functionName, n.Ip)

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
	c := http.Client{}
	n := GetNodes()
	all := make(map[Node][]byte)

	// request logs from all nodes
	// TODO what if one doesnt reply
	for _, node := range n {
		url := fmt.Sprintf("http://%s:%d/logs?name=%s", node.Ip, node.ManagerPort, ch.functionName)
		r, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			log.Printf("error building request %s", err.Error())
			return nil, err
		}
		res, err := c.Do(r)
		if err != nil {
			log.Printf("error performing request %d %s", res.StatusCode, err.Error())
			return nil, err
		}
		b, err := io.ReadAll(res.Body)
		if err != nil {
			log.Printf("error reading result body %s", err.Error())
			return nil, err
		}
		all[node] = b
	}

	// put it all in the buffer
	for _, b := range all {
		nw, err := io.WriteString(&logs, string(b))
		log.Printf("%d bytes written", nw)
		if err != nil {
			log.Printf("error writing to logs buffer %s", err.Error())
			return nil, err
		}
	}

	return &logs, nil
}

// Refresh is used when this function handler is already running (i.e. Create & Start were already called).
// It compares nodes stored in `nodes` to the nodes stored in the cluster registry.
func (ch *clusterHandler) refresh() []Node {

	log.Printf("fh %s knows about %d nodes\n", ch.functionName, len(ch.nodes))

	var new []Node

	// find new nodes
	nodes := GetNodes()
Outer:
	for _, node := range nodes {
		for _, oldNode := range ch.nodes {
			if oldNode.Ip == node.Ip {
				continue Outer
			}
		}
		new = append(new, node)
	}

	log.Printf("there are %d nodes in total\n", len(nodes))

	// if there are any, append to ch.nodes
	if len(new) != 0 {
		ch.nodes = append(ch.nodes, new...)
	}

	log.Printf("found %d nodes previously unknown to the fh\n", len(new))

	return new
}

// The function is then sent to the new nodes.
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

	// Empty slices and nil slices behave differently when marshalled and unmarshalled.
	// Sending a slice of length 0 to the nodes would result in an error, because 'envs'
	// couldn't be read into the struct in s.upload (in manager::main).
	// Replacing this with nil resolves the issue.
	if len(ch.envs) == 0 {
		zip["envs"] = nil
	}

	body, err := json.Marshal(zip)
	if err != nil {
		return err
	}

	// create the request
	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://%s:%d/upload", node.Ip, node.ManagerPort),
		bytes.NewReader(body),
	)
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
