package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/OpenFogStack/tinyFaaS/pkg/cluster"
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

const (
	ConfigPort          = 8080
	RProxyConfigPort    = 8081
	RProxyListenAddress = ""
	RProxyBin           = "./rproxy"
)

type server struct {
	ms *manager.ManagementService
}

func main() {

	// necessary to use in cluster handler without causing circular imports
	err := os.Setenv("CONFIG_PORT", strconv.Itoa(ConfigPort))
	if err != nil {
		panic(err)
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("manager: ")

	// todo also set this up for ConfigPort and RProxyConfigPort

	ports := map[string]int{
		"coap": 5683,
		"http": 8000,
		"grpc": 9000,
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
	// todo figure out why this doesnt see the value i set
	backend, ok := os.LookupEnv("TF_BACKEND")
	backend = "cluster"
	log.Println("backend =", backend)

	if !ok {
		backend = "docker"
		log.Println("using default backend docker")
	}

	var tfBackend manager.Backend
	switch backend {
	case "docker":
		log.Println("using docker backend")
		tfBackend = docker.New(id)
		// TODO add cluster backend
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
		RProxyConfigPort,
		tfBackend,
	)

	rproxyArgs := []string{fmt.Sprintf("%s:%d", RProxyListenAddress, RProxyConfigPort)}

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
	r.HandleFunc("/register", s.register) // todo rename registerHandler
	r.HandleFunc("/listnodes", s.listNodesHandler)
	r.HandleFunc("/echo", s.echoHandler)

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
	addr := fmt.Sprintf(":%d", ConfigPort)
	err = http.ListenAndServe(addr, r)
	if err != nil {
		log.Fatal(err)
	}
}

func (s *server) uploadHandler(w http.ResponseWriter, r *http.Request) {

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

	err := json.NewDecoder(r.Body).Decode(&d)
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

func (s *server) deleteHandler(w http.ResponseWriter, r *http.Request) {
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

	// delete function
	err = s.ms.Delete(d.FunctionName)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		return
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

func (s *server) wipeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err := s.ms.Wipe()

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *server) logsHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// parse request
	var logs io.Reader
	name := r.URL.Query().Get("name")

	if name == "" {
		l, err := s.ms.Logs()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println(err)
			return
		}
		logs = l
	}

	if name != "" {
		l, err := s.ms.LogsFunction(name)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println(err)
			return
		}
		logs = l
	}

	// return success
	w.WriteHeader(http.StatusOK)
	// w.Header().Set("Content-Type", "text/plain")
	_, err := io.Copy(w, logs)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		return
	}
}

func (s *server) urlUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
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
func (s *server) register(w http.ResponseWriter, r *http.Request) {

	log.Println("New register request")

	// ensure is post request
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// read relevant headers
	ip := r.Header.Get("Nodeip")
	managerPort := r.Header.Get("Managerport")
	rproxyPort := r.Header.Get("Rproxyport")

	log.Printf("trying to register %s %s %s\n", ip, managerPort, rproxyPort)

	mp, e1 := strconv.Atoi(managerPort)
	if e1 != nil {
		log.Println("couldnt read managerport")
		return
	}
	rp, e2 := strconv.Atoi(rproxyPort)
	if e2 != nil {
		log.Println("couldnt read rproxyport")
		return
	}
	if ip == "" || managerPort == "" || rproxyPort == "" || e1 != nil || e2 != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// TODO check if already registered => error
	// TODO check whether actual tinyfaas node

	// store node info
	cluster.Register(ip, mp, rp)

	// TODO if functions exist, upload all to the new node

	// respond positively to request
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) listNodesHandler(w http.ResponseWriter, r *http.Request) {

	nodes := cluster.GetNodes()
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
		Timeout: time.Duration(timeoutInt),
	}
	for _, node := range nodes {
		url := fmt.Sprintf("http://%s:%d", node.Ip, node.ManagerPort)
		log.Printf("sending ping to %s", url)
		req, err := http.NewRequest(http.MethodGet, url, strings.NewReader("hello world"))
		if err != nil {
			log.Printf("error building request for %s\n", url)
			results[node.Ip] = "could not build request"
			continue
		}
		start := time.Now()
		res, err := client.Do(req)
		end := time.Now()
		if err != nil {
			log.Printf("error occurred pinging %s\n", url)
			results[node.Ip] = "error with request"
			continue
		}
		log.Printf("ping result %s %s\n", url, res.Status)
		requestDuration := end.Sub(start)
		log.Printf("duration %s %s\n", url, requestDuration.String())
		results[node.Ip] = requestDuration.String()
	}

	// return result
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
	} else {
		w.WriteHeader(http.StatusOK)
	}
}
