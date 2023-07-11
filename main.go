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
	forceStatus bool
	locker      sync.RWMutex
	jobChannel  = make(chan *Job, 8192)
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

func sendToLogChannel(args ...string) {

	if logging {
		logChannel <- args
	}
}

// reqHandler handles any incoming http request. Its main purpose
// is to distribute the request further to all required caches.
func reqHandler(w http.ResponseWriter, r *http.Request) {

	var (
		groupName       string
		reqId           string
		broadcastCaches []Vcache
		reqStatusCode   = http.StatusOK
		respBody        = make(map[string]int)
	)

	for k, v := range r.Header {
		if strings.ToLower(k) == "x-group" {
			groupName = v[0]
			break
		}
	}

	if groupName == "" {
		locker.RLock()
		for _, c := range groups {
			broadcastCaches = append(broadcastCaches, c.Caches...)
		}
		locker.RUnlock()
	} else {
		locker.RLock()
		group, found := groups[groupName]
		locker.RUnlock()
		if !found {
			var errText = fmt.Sprintf("Group %s not found.", groupName)
			sendToLogChannel(errText)
			http.Error(w, errText, http.StatusNotFound)
			return
		}
		locker.RLock()
		broadcastCaches = group.Caches
		locker.RUnlock()
	}

	locker.RLock()
	var cacheCount = len(broadcastCaches)
	locker.RUnlock()

	if cacheCount == 0 {
		if groupName == "" {
			sendToLogChannel("No configured caches found.")
		} else {
			sendToLogChannel("Group ", groupName, " has no configured caches.")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var jobs = make([]*Job, cacheCount)

	locker.Lock()
	for idx, bc := range broadcastCaches {
		bc.Method = r.Method
		bc.Item = r.URL.Path
		bc.Headers = r.Header
		if len(r.Host) != 0 {
			bc.Headers.Add("Host", r.Host)
		}
		job := Job{}
		job.Cache = bc
		job.Result = make(chan []byte, 1)
		job.Status = make(chan int, 1)
		jobs[idx] = &job
		jobChannel <- &job
	}
	locker.Unlock()

	if logging {
		reqId = hash(hash(time.Now().String()))
	}

	for _, job := range jobs {
		jobStatusCode := <-job.Status
		if forceStatus && reqStatusCode == http.StatusOK {
			reqStatusCode = jobStatusCode
		}
		locker.Lock()
		respBody[job.Cache.Name] = jobStatusCode
		locker.Unlock()
		locker.RLock()
		sendToLogChannel(reqId, " ", r.Method, " ", job.Cache.Address, r.URL.Path, " ", "\n")
		locker.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(reqStatusCode)

	out, _ := json.MarshalIndent(respBody, "", "  ")
	w.Write(out)
}

/*
 * crt: certificate
 * key: private key
 * port: the http port
 * https: the https port
 */
func startBroadcastServer(crt *string, key *string, port *int, https *int) {

	http.HandleFunc("/", reqHandler)
	if *crt != "" && *key != "" {
		_, err := os.Stat(*crt)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		_, err = os.Stat(*key)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s Broadcaster serving on %s...\n", time.Now().Format(time.RFC3339), strconv.Itoa(*https))
		fmt.Println(http.ListenAndServeTLS(":"+strconv.Itoa(*https), *crt, *key, nil))
	} else {
		fmt.Fprintf(os.Stdout, "%s Broadcaster serving on %s...\n", time.Now().Format(time.RFC3339), strconv.Itoa(*port))
		fmt.Println(http.ListenAndServe(":"+strconv.Itoa(*port), nil))
	}
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
	)

	// Be nice and do not use all available threads.
	runtime.GOMAXPROCS(runtime.NumCPU() - 1)

	// Set Global variable forceStatus
	forceStatus = *enforceStatus

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
	go logger(*logFilePath, logChannel, muteChannel)

	// Relay SIGHUP signals to the hupChannel and start
	// a thread to handle configuration reloads.
	signal.Notify(hupChannel, syscall.SIGHUP)
	go reloadConfigOnHangUp(cachesCfgFile, hupChannel, logChannel, grpChannel)

	// Load the initial configuration.
	syscall.Kill(os.Getpid(), syscall.SIGHUP)

	// Now set logging according to the CLI parameters.
	muteChannel <- *enableLog

	// Relay Interrupts, TERM and ABRT signals to the kilChannel
	// and start thread to handle graceful shutdown.
	signal.Notify(kilChannel, os.Interrupt, syscall.SIGTERM, syscall.SIGABRT)
	go gracefulTerminate(logChannel, hupChannel, kilChannel, grpChannel, muteChannel)

	// Start worker threads.
	for i := 0; i < (*grCount); i++ {
		go jobWorker(jobChannel, reqRetries)
	}

	startBroadcastServer(crtFile, keyFile, port, httpsPort)
}
