package cluster

import (
	"log"
	"sync"
)

// When /register is called at the management service, it stores the endpoints in this slice
// The slice is in its own file to prevent circular dependencies between cmd.manager.main.go
// and pkg.cluster.handler.go
var nodes []Node
var Lock = &sync.Mutex{}

type Node struct {
	ip          string
	managerPort int
	rproxyPort  int
}

func NewNode(ip string, managerPort, rproxyPort int) *Node {
	return &Node{
		ip,
		managerPort,
		rproxyPort,
	}
}

func Register(endpoint string, managerPort, rproxyPort int) {
	Lock.Lock()
	nodes = append(nodes, *NewNode(endpoint, managerPort, rproxyPort))
	Lock.Unlock()
	log.Println("tinyfaas node %s:(%d/%d) registered", endpoint, managerPort, rproxyPort)
}

func GetNodes() []Node {
	return nodes
}
