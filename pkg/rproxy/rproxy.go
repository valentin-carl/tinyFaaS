package rproxy

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sync"
)

type Status uint32

const (
	StatusOK Status = iota
	StatusAccepted
	StatusNotFound
	StatusError
)

type RProxy struct {
	Hosts map[string][]string
	hl    sync.RWMutex
}

func New() *RProxy {
	return &RProxy{
		Hosts: make(map[string][]string),
	}
}

// adds a new function to Hosts map
func (r *RProxy) Add(name string, ips []string) error {
	if len(ips) == 0 {
		return fmt.Errorf("no ips given")
	}

	r.hl.Lock()
	defer r.hl.Unlock()

	// TODO why is this commented out?
	// if function exists, we should update!
	// if _, ok := r.Hosts[name]; ok {
	// 	return fmt.Errorf("function already exists")
	// }

	r.Hosts[name] = ips
	return nil
}

// removes a function
func (r *RProxy) Del(name string) error {
	r.hl.Lock()
	defer r.hl.Unlock()

	if _, ok := r.Hosts[name]; !ok {
		return fmt.Errorf("function not found")
	}

	delete(r.Hosts, name)
	return nil
}

func (r *RProxy) Call(name string, payload []byte, async bool) (Status, []byte) {

	handler, ok := r.Hosts[name]

	if !ok {
		log.Printf("function not found: %s", name)
		return StatusNotFound, nil
	}

	log.Printf("have handlers: %s", handler)

	// choose random handler
	h := handler[rand.Intn(len(handler))]

	log.Printf("chosen handler: %s", h)

	// call function
	if async {
		log.Printf("async request accepted")
		go func() {
			resp, err := http.Post(fmt.Sprintf("http://%s:8000/fn", h), "application/binary", bytes.NewBuffer(payload))

			if err != nil {
				return
			}

			resp.Body.Close()

			log.Printf("async request finished")
		}()
		return StatusAccepted, nil
	}

	// call function and return results
	log.Printf("sync request starting")
	resp, err := http.Post(fmt.Sprintf("http://%s:8000/fn", h), "application/binary", bytes.NewBuffer(payload))

	if err != nil {
		log.Print(err)
		return StatusError, nil
	}

	log.Printf("sync request finished")

	defer resp.Body.Close()
	res_body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Print(err)
		return StatusError, nil
	}

	// log.Printf("have response for sync request: %s", res_body)

	return StatusOK, res_body
}
