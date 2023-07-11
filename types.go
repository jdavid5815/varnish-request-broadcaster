package main

import (
	"net/http"
)

// Varnish cache
type Vcache struct {
	Name    string       `json:"name"`
	Address string       `json:"address"`
	Method  string       `json:"-"`
	Item    string       `json:"-"`
	Headers http.Header  `json:"-"`
	Client  *http.Client `json:"-"`
}

type Group struct {
	Name   string   `json:"name"`
	Caches []Vcache `json:"caches"`
}

type Job struct {
	Cache  Vcache
	Status chan int
	Result chan []byte
}
