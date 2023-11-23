package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxIdleConnections int = 100
	requestTimeout     int = 5
)

var (
	wg sync.WaitGroup
	rw sync.RWMutex
)

func createHTTPClient() *http.Client {

	defaultLocalAddr := net.IPAddr{IP: net.IPv4zero}
	d := &net.Dialer{
		LocalAddr: &net.TCPAddr{IP: defaultLocalAddr.IP, Zone: defaultLocalAddr.Zone},
		KeepAlive: 2 * time.Minute,
		Timeout:   30 * time.Second,
	}
	client := &http.Client{
		Transport: &http.Transport{
			DisableCompression:  true,
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConnsPerHost: maxIdleConnections,
			DisableKeepAlives:   false,
			Dial:                d.Dial,
		},
		Timeout: time.Duration(requestTimeout) * time.Second,
	}
	return client
}

func hash(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("%v", h.Sum32())
}

/* startBroadcastServer starts up an HTTP server that accepts either http or https
 * connections, depending on whether crt and key are empty or not. If https is chosen
 * then port https is used. If not port http is used. Log messages are send to the lc
 * logChannel.
 */
func startBroadcastServer(crt string, key string, port int, https int, forceStatus bool, lc chan<- []string, gc chan map[string]Group, jc chan Job) {

	var (
		groups map[string]Group
		err    error
	)

	// Wait for the groups configuration.
	groups = <-gc
	//http.HandleFunc("/", reqHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		var (
			groupName       string
			errText         string
			cacheCount      int
			broadcastCaches []Vcache
			jobs            []Job
			reqStatusCode   = http.StatusOK
			respBody        = make(map[string]int)
		)

		rw.RLock()
		for k, v := range r.Header {
			if strings.ToLower(k) == "x-group" {
				groupName = v[0]
				break
			}
		}
		rw.RUnlock()
		select {
		case groups = <-gc:
		default:
		}
		if groupName == "" {
			// No specific group chosen, so relay to all caches.
			for _, c := range groups {
				broadcastCaches = append(broadcastCaches, c.Caches...)
			}
		} else {
			group, found := groups[groupName]
			if !found {
				errText = fmt.Sprintf("Group %s not found.", groupName)
				lc <- []string{errText}
				rw.Lock()
				http.Error(w, errText, http.StatusNotFound)
				rw.Unlock()
				return
			}
			broadcastCaches = group.Caches
		}
		cacheCount = len(broadcastCaches)
		if cacheCount == 0 {
			if groupName == "" {
				lc <- []string{"No configured caches found."}
			} else {
				lc <- []string{fmt.Sprintf("Group %s has no configured caches.", groupName)}
			}
			rw.Lock()
			w.WriteHeader(http.StatusNoContent)
			rw.Unlock()
			return
		}
		jobs = make([]Job, cacheCount)
		for idx, bc := range broadcastCaches {
			rw.RLock()
			bc.Method = r.Method
			bc.Item = r.URL.Path
			bc.Headers = r.Header
			if len(r.Host) != 0 {
				bc.Headers.Add("Host", r.Host)
			}
			rw.RUnlock()
			job := Job{}
			job.Cache = bc
			job.Result = make(chan []byte, 1)
			job.Status = make(chan int, 1)
			jobs[idx] = job
			jc <- job
		}
		for _, job := range jobs {
			jobStatusCode := <-job.Status
			if forceStatus && reqStatusCode == http.StatusOK {
				reqStatusCode = jobStatusCode
			}
			respBody[job.Cache.Name] = jobStatusCode
			rw.RLock()
			lc <- []string{hash(hash(time.Now().String())), " ", r.Method, " ", job.Cache.Address, r.URL.Path, " ", "\n"}
			rw.RUnlock()
		}
		rw.Lock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reqStatusCode)
		out, _ := json.MarshalIndent(respBody, "", "  ")
		w.Write(out)
		rw.Unlock()
	})
	if crt != "" && key != "" {
		_, err = os.Stat(crt)
		if err != nil {
			lc <- []string{err.Error()}
			syscall.Kill(os.Getpid(), syscall.SIGTERM)
			return
		}
		_, err = os.Stat(key)
		if err != nil {
			lc <- []string{err.Error()}
			syscall.Kill(os.Getpid(), syscall.SIGTERM)
			return
		}
		lc <- []string{fmt.Sprintf("Broadcaster serving on %s...\n", strconv.Itoa(https))}
		err = http.ListenAndServeTLS(":"+strconv.Itoa(https), crt, key, nil)
	} else {
		lc <- []string{fmt.Sprintf("Broadcaster serving on %s...\n", strconv.Itoa(port))}
		err = http.ListenAndServe(":"+strconv.Itoa(port), nil)
	}
	lc <- []string{err.Error()}
}

func main() {

	var (
		commandLine   = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
		port          = commandLine.Int("port", 8088, "Broadcaster port.")
		httpsPort     = commandLine.Int("https-port", 8443, "Broadcaster https port.")
		grCount       = commandLine.Int("goroutines", 8, "Job handling goroutines pool. Higher is not implicitly better!")
		reqRetries    = commandLine.Int("retries", 1, "Request retry times against a cache - should the first attempt fail.")
		cachesCfgFile = commandLine.String("cfg", "/caches.ini", "Path pointing to the caches configuration file.")
		logFilePath   = commandLine.String("log-file", "", "Log file path.")
		enforceStatus = commandLine.Bool("enforce", false, "Enforces the status code of a request to be the first encountered non-200 received from a cache. Disabled by default.")
		enableLog     = commandLine.Bool("enable-log", false, "Switches logging on/off. Disabled by default.")
		crtFile       = commandLine.String("crt", "", "CRT file used for HTTPS support.")
		keyFile       = commandLine.String("key", "", "KEY file used for HTTPS support.")
		logChannel    = make(chan []string, 8192)
		hupChannel    = make(chan os.Signal, 1)
		kilChannel    = make(chan os.Signal, 1)
		grpChannel    = make(chan map[string]Group, 1)
		muteChannel   = make(chan bool, 1)
		jobChannel    = make(chan Job, 8192)
	)

	// Be nice and do not use all available threads.
	runtime.GOMAXPROCS(runtime.NumCPU() - 1)

	commandLine.Usage = func() {
		fmt.Fprint(os.Stdout, "Usage of the varnish broadcaster:\n")
		commandLine.PrintDefaults()
	}

	if err := commandLine.Parse(os.Args[1:]); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	if *cachesCfgFile == "" {
		fmt.Println("No configuration file specified. Use the -cfg parameter to specify one.")
		os.Exit(1)
	}

	// Start logger thread.
	wg.Add(1)
	go logger(*logFilePath, logChannel, muteChannel)

	// Relay SIGHUP signals to the hupChannel and start
	// a thread to handle configuration reloads.
	signal.Notify(hupChannel, syscall.SIGHUP)
	wg.Add(1)
	go reloadConfigOnHangUp(cachesCfgFile, hupChannel, logChannel, grpChannel)

	// Relay Interrupts, TERM and ABRT signals to the kilChannel
	// and start thread to handle graceful shutdown.
	signal.Notify(kilChannel, os.Interrupt, syscall.SIGTERM, syscall.SIGABRT)
	wg.Add(1)
	go gracefulTerminate(logChannel, hupChannel, kilChannel, grpChannel, muteChannel)

	// Allow some time for the different threads to properly start.
	time.Sleep(100 * time.Millisecond)

	// Load the initial configuration.
	syscall.Kill(os.Getpid(), syscall.SIGHUP)

	// Start worker threads.
	for i := 0; i < (*grCount); i++ {
		wg.Add(1)
		go jobWorker(jobChannel, *reqRetries)
	}

	// Now set logging according to the CLI parameters.
	muteChannel <- *enableLog

	startBroadcastServer(*crtFile, *keyFile, *port, *httpsPort, *enforceStatus, logChannel, grpChannel, jobChannel)

	// Wait for other threads to gracefully terminate.
	wg.Wait()
}
