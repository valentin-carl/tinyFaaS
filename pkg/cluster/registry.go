package cluster

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
)

// When /register is called at the management service, it stores the endpoints in this slice
// The slice is in its own file to prevent circular dependencies between cmd.manager.main.go
// and pkg.cluster.handler.go
var nodes []Node
var lock = &sync.Mutex{}

type Node struct {
	Ip          string `json:"ip"`
	ManagerPort int    `json:"manager_port"` // todo is this used anywhere? if not, delete it
	RproxyPort  int    `json:"rproxy_port"`  // todo is this used anywhere? if not, delete it
}

func (n *Node) String() string {
	return fmt.Sprintf(`{"nodeip": "%s", "managerPort": "%d", "RproxyPort": "%d"}`, n.Ip, n.ManagerPort, n.RproxyPort)
}

func NewNode(ip string, managerPort, rproxyPort int) *Node {
	return &Node{
		ip,
		managerPort,
		rproxyPort,
	}
}

func Register(endpoint string, managerPort, rproxyPort int) error {

	// avoid concurrent access to nodes slice
	lock.Lock()
	defer lock.Unlock()

	// check whether node is already registered
	if n := (Node{endpoint, managerPort, rproxyPort}); slices.Contains(nodes, n) {
		msg := "this node is already registered"
		log.Println(msg)
		return errors.New(msg)
	}

	// register node
	nodes = append(nodes, *NewNode(endpoint, managerPort, rproxyPort))
	log.Printf("tinyfaas node %s:(%d/%d) registered\n", endpoint, managerPort, rproxyPort)
	return nil
}

func GetNodes() []Node {
	return nodes
}

func IsRegistered(ip string, mport, rport int) bool {
	lock.Lock()
	defer lock.Unlock()
	return slices.Contains(nodes, Node{ip, mport, rport})
}

func DeleteNode(ip string, mport, rport int) error {
	lock.Lock()
	defer lock.Unlock()

	// find node
	index := -1
	for i := range nodes {
		if nodes[i] == (Node{ip, mport, rport}) {
			index = i
			break
		}
	}
	if index == -1 {
		return errors.New("no such node")
	}

	// delete it
	nodes[index] = nodes[len(nodes)-1]
	nodes[len(nodes)-1] = Node{}
	nodes = nodes[:len(nodes)-1]
	return nil
}
