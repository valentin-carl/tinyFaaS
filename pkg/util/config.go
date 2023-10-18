package util

import (
	"encoding/json"
	"io"
	"log"
	"os"
)

type Config struct {
	ConfigPort       int `json:"ConfigPort"`
	RProxyConfigPort int `json:"RProxyConfigPort"`
	Ports            struct {
		Coap int `json:"coap"`
		Http int `json:"http"`
		Grpc int `json:"grpc"`
	} `json:"Ports"`
}

var DefaultConfig Config = Config{
	ConfigPort:       8080,
	RProxyConfigPort: 8081,
	Ports: struct {
		Coap int `json:"coap"`
		Http int `json:"http"`
		Grpc int `json:"grpc"`
	}{
		5683,
		8000,
		9000,
	},
}

// LoadConfig assumes there is a `config.json` file in the tinyFaaS directory.
// If there is no such file, or if the json cannot be unmarshalled, an error is returned.
func LoadConfig() (Config, error) {

	// load file
	file, err := os.Open("./config.json") // relative path with respect to the tinyFaaS directory
	if err != nil {
		log.Println("could not open config file", err.Error())
		return Config{}, err
	}

	// read bytes
	bytes, err := io.ReadAll(file)
	if err != nil {
		log.Println("could not open config file", err.Error())
		return Config{}, err
	}

	// unmarshall
	var config Config
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		log.Println("could not unmarshall config contents", err.Error())
		return Config{}, err
	}

	return config, nil
}
