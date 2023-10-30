package cluster

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// fn i.e. what shoud be called, e.g., http://localhost:1234/list => fn would be list
func RequestAll(method, fn, body string) (int, error) {

	client := http.Client{}
	for _, node := range nodes {

		// new request
		url := fmt.Sprintf("http://%s:%d/%s", node.Ip, node.ManagerPort, fn)
		var b io.Reader
		if body == "" {
			b = nil
		} else {
			b = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, url, b)
		if err != nil {
			log.Printf("error creating request %s", err.Error())
			return http.StatusInternalServerError, err
		}

		// send it
		res, err := client.Do(req)
		if err != nil || res.StatusCode != http.StatusOK {
			log.Printf("error sending request or with node %d %s", res.StatusCode, err.Error())
			return res.StatusCode, err
		}
	}

	return http.StatusOK, nil
}
