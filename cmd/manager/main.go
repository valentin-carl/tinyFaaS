package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/OpenFogStack/tinyFaaS/pkg/cluster"
	"github.com/OpenFogStack/tinyFaaS/pkg/util"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/OpenFogStack/tinyFaaS/pkg/docker"
	"github.com/OpenFogStack/tinyFaaS/pkg/manager"
	"github.com/google/uuid"
)

var Config util.Config // todo

const (
	RProxyListenAddress = ""
	RProxyBin           = "./rproxy"
)

type server struct {
	ms *manager.ManagementService
}

func main() {

	// read the Config file if there is one, otherwise go back to default
	Config, err := util.LoadConfig()
	if err != nil {
		log.Println("Using default config")
		Config = util.DefaultConfig
	} else {
		log.Println("loaded config", Config.RProxyConfigPort, Config.ConfigPort, Config.Ports)
	}

	// necessary to use in cluster handler without causing circular imports
	err = os.Setenv("CONFIG_PORT", strconv.Itoa(Config.ConfigPort))
	if err != nil {
		panic(err)
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("manager: ")

	ports := map[string]int{
		"coap": Config.Ports.Coap,
		"http": Config.Ports.Http,
		"grpc": Config.Ports.Grpc,
	}

	for p := range ports {
		portstr := os.Getenv(p + "_PORT")

		if portstr == "" {
			continue
		}

		port, err := strconv.Atoi(portstr)

		if err != nil {
			log.Fatalf("invalid port for protocol %s: %s (must be an integer!)", p, err)
		}

		if port < 0 {
			delete(ports, p)
			continue
		}

		if port > 65535 {
			log.Fatalf("invalid port for protocol %s: %s (must be an integer lower than 65535!)", p, err)
		}

		ports[p] = port
	}

	// setting backend to docker
	id := uuid.New().String()

	// find backend
	backend, ok := os.LookupEnv("TF_BACKEND")

	if !ok {
		backend = "docker"
		log.Println("using default backend docker")
	}

	log.Println("backend =", backend)

	var tfBackend manager.Backend
	switch backend {
	case "docker":
		log.Println("using docker backend")
		tfBackend = docker.New(id)
	case "cluster":
		log.Println("using cluster backend")
		tfBackend = cluster.New(id)
	default:
		log.Fatalf("invalid backend %s", backend)
	}

	ms := manager.New(
		id,
		RProxyListenAddress,
		ports,
		Config.RProxyConfigPort,
		tfBackend,
	)

	rproxyArgs := []string{fmt.Sprintf("%s:%d", RProxyListenAddress, Config.RProxyConfigPort)}

	for prot, port := range ports {
		rproxyArgs = append(rproxyArgs, fmt.Sprintf("%s:%s:%d", prot, RProxyListenAddress, port))
	}

	log.Println("rproxy args:", rproxyArgs)
	c := exec.Command(RProxyBin, rproxyArgs...)

	stdout, err := c.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}

	stderr, err := c.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

	err = c.Start()
	if err != nil {
		log.Fatal(err)
	}

	rproxy := c.Process

	log.Println("started rproxy")

	s := &server{
		ms: ms,
	}

	// create handlers
	r := http.NewServeMux()
	r.HandleFunc("/upload", s.uploadHandler)
	r.HandleFunc("/delete", s.deleteHandler)
	r.HandleFunc("/list", s.listHandler)
	r.HandleFunc("/wipe", s.wipeHandler)
	r.HandleFunc("/logs", s.logsHandler)
	r.HandleFunc("/uploadURL", s.urlUploadHandler)
	// cluster api
	r.HandleFunc("/cluster/register", s.registerHandler) // register a new node
	r.HandleFunc("/cluster/list", s.listNodesHandler)    // list all registered nodes
	r.HandleFunc("/cluster/echo", s.echoHandler)         // ping a node's manager (for /cluster/health)
	r.HandleFunc("/cluster/health", s.pingNodes)         // ping all registered nodes and measure response time
	r.HandleFunc("/cluster/delete", s.deleteNode)        // delete a node

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig

		log.Println("received interrupt")
		log.Println("shutting down")

		// stop rproxy
		log.Println("stopping rproxy")
		err := rproxy.Kill()

		if err != nil {
			log.Println(err)
		}

		// stop handlers
		log.Println("stopping management service")
		err = ms.Stop()

		if err != nil {
			log.Println(err)
		}

		os.Exit(0)
	}()

	// start server
	log.Println("starting HTTP server")
	addr := fmt.Sprintf(":%d", Config.ConfigPort)
	err = http.ListenAndServe(addr, r)
	if err != nil {
		log.Fatal(err)
	}
}

func (s *server) uploadHandler(w http.ResponseWriter, r *http.Request) {

	defer r.Body.Close()

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// parse request
	d := struct {
		FunctionName    string   `json:"name"`
		FunctionEnv     string   `json:"env"`
		FunctionThreads int      `json:"threads"`
		FunctionZip     string   `json:"zip"`
		FunctionEnvs    []string `json:"envs"`
	}{}

	sttt, e := io.ReadAll(r.Body)
	log.Println(string(sttt))
	if e != nil {
		log.Println(e.Error())
	}

	err := json.NewDecoder(bytes.NewReader(sttt)).Decode(&d)
	//err := json.NewDecoder(r.Body).Decode(&d)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Println(err)
		return
	}

	log.Println("got request to upload function: Name", d.FunctionName, "Env", d.FunctionEnv, "Threads", d.FunctionThreads, "Bytes", len(d.FunctionZip), "Envs", d.FunctionEnvs)

	envs := make(map[string]string)
	for _, e := range d.FunctionEnvs {
		k, v, ok := strings.Cut(e, "=")

		if !ok {
			log.Println("invalid env:", e)
			continue
		}

		envs[k] = v
	}

	res, err := s.ms.Upload(d.FunctionName, d.FunctionEnv, d.FunctionThreads, d.FunctionZip, envs)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		return
	}

	// return success
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, res)

}

// delete a function (not node!)
func (s *server) deleteHandler(w http.ResponseWriter, r *http.Request) {

	// validate request
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// parse request
	d := struct {
		FunctionName string `json:"name"`
	}{}
	err := json.NewDecoder(r.Body).Decode(&d)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Println(err)
		return
	}

	log.Println("got request to delete function:", d.FunctionName)

	// for cluster mode
	if be := os.Getenv("TF_BACKEND"); be == "cluster" {

		log.Println("delete: entered cluster mode delete version")
		client := http.Client{}

		// send delete request to all registered nodes
		for _, node := range cluster.GetNodes() {

			log.Printf("sending request to delete %s to %s:%d", d.FunctionName, node.Ip, node.ManagerPort)

			url := fmt.Sprintf("http://%s:%d/delete", node.Ip, node.ManagerPort)
			b, err := json.Marshal(d)
			if err != nil {
				log.Printf("could not marshall function name")
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			r, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
			if err != nil {
				log.Printf("error creating new request")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			res, err := client.Do(r)
			if err != nil || res.StatusCode != http.StatusOK {
				log.Printf("error sending request or status not ok")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			log.Println(res.StatusCode)
		}

	} else {
		// docker mode
		// delete function
		err = s.ms.Delete(d.FunctionName)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println(err)
			return
		}
	}

	// return success
	w.WriteHeader(http.StatusOK)
}

func (s *server) listHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	l := s.ms.List()

	// return success
	w.WriteHeader(http.StatusOK)
	for _, f := range l {
		fmt.Fprintf(w, "%s\n", f)
	}
}

// removes all functions
func (s *server) wipeHandler(w http.ResponseWriter, r *http.Request) {

	// validate request
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// cluster mode
	if be := os.Getenv("TF_BACKEND"); be == "cluster" {
		client := http.Client{}
		for _, node := range cluster.GetNodes() {
			// create request
			url := fmt.Sprintf("http://%s:%d/wipe", node.Ip, node.ManagerPort)
			req, err := http.NewRequest(http.MethodPost, url, nil)
			if err != nil {
				fmt.Println("error creating request")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// perform request
			res, err := client.Do(req)
			if err != nil || res.StatusCode != http.StatusOK {
				log.Printf("error sending request %d", res.StatusCode)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			log.Printf("telling %s:%d to wipe res %d", node.Ip, node.ManagerPort, res.StatusCode)
		}
	} else {
		// docker mode
		// tell local to remove all functions
		err := s.ms.Wipe()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println(err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) logsHandler(w http.ResponseWriter, r *http.Request) {

	log.Println("fetching logs")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// parse request
	var logs io.Reader
	name := r.URL.Query().Get("name")

	log.Printf("name = %s", name)

	if name == "" {

		log.Println("no function name specified")

		l, err := s.ms.Logs()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println(err)
			return
		}
		logs = l
	}

	if name != "" {

		log.Printf("name %s specified, trying to get logs", name)

		l, err := s.ms.LogsFunction(name)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println(err)
			return
		}

		logs = l
	} else {
		// if the name parameter is missing, no logs were requested
		log.Println("no function requested, query parameters missing")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// return success
	w.WriteHeader(http.StatusOK)
	// w.Header().Set("Content-Type", "text/plain")
	n, err := io.Copy(w, logs)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		return
	}
	log.Printf("%d bytes written", n)
}

func (s *server) urlUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// todo check out what the local case returns in response body and maybe do something similar
	if be := os.Getenv("TF_BACKEND"); be == "cluster" {

		// read request body
		b, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("error reading request body %s", err.Error())
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		client := http.Client{}

		for _, node := range cluster.GetNodes() {

			// make request
			url := fmt.Sprintf("http://%s:%d/uploadURL", node.Ip, node.ManagerPort)
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b)) // todo do I have to close this??
			if err != nil {
				log.Println("error creating request", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// send it
			res, err := client.Do(req)
			if err != nil || res.StatusCode != http.StatusOK {
				log.Printf("error sending request to %s:%d %s", node.Ip, node.ManagerPort, err.Error())
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		return // end function here if in cluster mode
	}

	// parse request
	d := struct {
		FunctionName    string   `json:"name"`
		FunctionEnv     string   `json:"env"`
		FunctionThreads int      `json:"threads"`
		FunctionURL     string   `json:"url"`
		FunctionEnvs    []string `json:"envs"`
		SubFolder       string   `json:"subfolder_path"`
	}{}

	err := json.NewDecoder(r.Body).Decode(&d)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Println(err)
		return
	}

	log.Println("got request to upload function:", d)

	envs := make(map[string]string)
	for _, e := range d.FunctionEnvs {
		k, v, ok := strings.Cut(e, "=")

		if !ok {
			log.Println("invalid env:", e)
			continue
		}

		envs[k] = v
	}

	res, err := s.ms.UrlUpload(d.FunctionName, d.FunctionEnv, d.FunctionThreads, d.FunctionURL, d.SubFolder, envs)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		return
	}

	// return success
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, res)
}

// In cluster mode, other tinyFaaS instances can be registered at the main node.
// The request needs a header `endpoint`, which will be stored and used to
// communicate with the to-be-registered node.
// todo it might be better if the data is sent in the request body
func (s *server) registerHandler(w http.ResponseWriter, r *http.Request) {

	log.Println("New register request")

	// ensure is post request
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// read relevant headers
	log.Println(r.Header)
	ip := r.Header.Get("nodeip")
	managerPort := r.Header.Get("managerport")
	rproxyPort := r.Header.Get("rproxyport")

	log.Printf("trying to register %s %s %s\n", ip, managerPort, rproxyPort)

	mp, e1 := strconv.Atoi(managerPort)
	if e1 != nil {
		log.Println("couldnt read managerport")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	rp, e2 := strconv.Atoi(rproxyPort)
	if e2 != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Println("couldnt read rproxyport")
		return
	}
	if ip == "" || managerPort == "" || rproxyPort == "" || e1 != nil || e2 != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// check whether its an actual tinyfaas node
	// try /echo endpoint => if correct response comes back, it's probably another TF node
	/*res, err := http.Post(fmt.Sprintf("http://%s:%s/cluster/echo", ip, managerPort), "text/plain", strings.NewReader("hello world"))
	log.Println(err.Error())
	if err != nil || res.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusBadRequest)
		sc := -1
		if res != nil {
			sc = res.StatusCode
		}
		log.Printf("could not confirm %s:%s is a tinyfaas node %d %s", ip, managerPort, sc, err.Error())
		return
	}*/

	// store node info
	err := cluster.Register(ip, mp, rp)
	if err != nil {
		log.Println("Node already registered")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// TODO if functions exist, upload all to the new node

	// respond positively to request
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) listNodesHandler(w http.ResponseWriter, r *http.Request) {

	nodes := cluster.GetNodes()
	log.Printf("%p %d", nodes, len(nodes))

	log.Printf(`"listNodesHandler" got %d nodes`, len(nodes))

	msg, err := json.Marshal(nodes)
	log.Printf("nodes: %s\n", msg)
	if err != nil {
		fmt.Println("could not marshall nodes slice")
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/json")
	_, err = w.Write(msg)
	if err != nil {
		log.Printf("Something went wrong while trying to write response body %s\n", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (s *server) echoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	x, _ := io.ReadAll(r.Body)
	_, err := w.Write(x)
	if err != nil {
		panic(err)
	}
}

func (s *server) pingNodes(w http.ResponseWriter, r *http.Request) {

	// check for timeout header (default 5 sec)
	timeout := r.Header.Get("timeout")
	if timeout == "" {
		log.Println("no timeout header, defaulting to 5 sec")
		timeout = "5"
	}
	timeoutInt, err := strconv.Atoi(timeout)
	if err != nil {
		log.Printf("Problem with reading timeout header %s\n", timeout)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// store map with response time (/"request timeout")
	nodes := cluster.GetNodes()
	results := make(map[string]string)
	client := http.Client{
		Timeout: time.Duration(timeoutInt) * time.Second,
	}
	for _, node := range nodes {
		url := fmt.Sprintf("http://%s:%d/cluster/echo", node.Ip, node.ManagerPort)
		log.Printf("sending ping to %s", url)
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("hello world"))
		if err != nil {
			log.Printf("error building request for %s %s\n", url, err.Error())
			results[node.Ip] = "could not build request"
			continue
		}
		start := time.Now()
		res, err := client.Do(req)
		end := time.Now()
		if err != nil {
			log.Printf("error occurred pinging %s %s\n", url, err.Error())
			results[node.Ip] = "error with request"
			continue
		}
		log.Printf("ping result %s %s\n", url, res.Status)
		requestDuration := end.Sub(start)
		log.Printf("duration %s %s\n", url, requestDuration.String())
		results[node.Ip] = requestDuration.String()
	}

	log.Println(results, len(results))

	// return result
	if len(results) == 0 {
		log.Println("result is empty")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	bodyJson, err := json.Marshal(results)
	if err != nil {
		log.Println("could marshall results")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, err = w.Write(bodyJson)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (s *server) deleteNode(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Println(r.Header, r.Method)

	// read relevant headers
	ip := r.Header.Get("nodeip")
	mportStr := r.Header.Get("managerport")
	rportStr := r.Header.Get("rproxyport")
	mport, e1 := strconv.Atoi(mportStr)
	rport, e2 := strconv.Atoi(rportStr)
	if ip == "" || mportStr == "" || rportStr == "" || e1 != nil || e2 != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// check whether the node is actually registered
	if !cluster.IsRegistered(ip, mport, rport) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// delete it
	err := cluster.DeleteNode(ip, mport, rport)
	if err != nil {
		log.Println(err.Error())
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// success
	w.WriteHeader(http.StatusOK)
}
