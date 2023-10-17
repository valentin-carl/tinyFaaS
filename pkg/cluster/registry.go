package cluster

import (
	"log"
	"sync"
)

// When /register is called at the management service, it stores the endpoints in this slice
// The slice is in its own file to prevent circular dependencies between cmd.manager.main.go
// and pkg.cluster.handler.go
var nodes []Node
var lock = &sync.Mutex{}

type Node struct {
	Ip          string `json:"ip"`
	ManagerPort int    `json:"manager_port"`
	RproxyPort  int    `json:"rproxy_port"`
}

func NewNode(ip string, managerPort, rproxyPort int) *Node {
	return &Node{
		ip,
		managerPort,
		rproxyPort,
	}
}

func Register(endpoint string, managerPort, rproxyPort int) {
	lock.Lock()
	nodes = append(nodes, *NewNode(endpoint, managerPort, rproxyPort))
	lock.Unlock()
	log.Printf("tinyfaas node %s:(%d/%d) registered\n", endpoint, managerPort, rproxyPort)
}

func GetNodes() []Node {
	return nodes
}
