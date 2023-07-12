package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// logger listens on lc for an incoming array of strings
// and writes those strings to either Stdout, or if the path
// variable is not equal to the empty string, to a logfile
// defined by path. It will also check for a boolean value on the
// off channel and if true, will disable any logging.
func logger(path string, lc <-chan []string, mute <-chan bool) {

	var (
		logBuffer bytes.Buffer
		logger    *log.Logger
		enabled   bool = true
	)

	defer wg.Done()
	if path != "" {
		logWriter, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		defer logWriter.Close()
		logger = log.New(logWriter, "", 0)
	} else {
		logger = log.New(os.Stdout, "", 0)
	}
	for logEntry := range lc {
		select {
		case enabled = <-mute:
			if enabled {
				logBuffer.Reset()
				logBuffer.WriteString(time.Now().Format(time.RFC3339))
				logBuffer.WriteString(" ")
				for _, logString := range logEntry {
					logBuffer.WriteString(logString)
				}
				logger.Print(logBuffer.String())
			}
		default:
			if enabled {
				logBuffer.Reset()
				logBuffer.WriteString(time.Now().Format(time.RFC3339))
				logBuffer.WriteString(" ")
				for _, logString := range logEntry {
					logBuffer.WriteString(logString)
				}
				logger.Print(logBuffer.String())
			}
		}
	}
}

// reloadConfigOnHangUp waits for a hang-up signal to arrive on hc.
// When such a signal arrives, the configuration is reloaded from disk
// and a log message is send to lc. If the new configuration is successfully
// processed, the results will be send to gc.
func reloadConfigOnHangUp(caches *string, hc <-chan os.Signal, lc chan<- []string, gc chan<- map[string]Group) {

	var groups map[string]Group

	defer wg.Done()
Start:
	for range hc {
		lc <- []string{"Loading configuration & setting up connections.\n"}
		groups = make(map[string]Group)
		groupList, err := LoadCachesFromIni(*caches)
		if err != nil {
			lc <- []string{err.Error()}
			continue
		}
		for _, g := range groupList {
			for i := 0; i < len(g.Caches); i++ {
				_, err = url.Parse(g.Caches[i].Address)
				if err != nil {
					lc <- []string{err.Error()}
					break Start
				}
				g.Caches[i].Client = createHTTPClient()
			}
			groups[g.Name] = g
		}
		gc <- groups
	}
}

// gracefulTerminate waits for an Interrupt, TERM or ABRT signal to arrive on the kill channel
// and then closes all the other channel before exiting.
func gracefulTerminate(log chan<- []string, hup chan<- os.Signal, kill chan os.Signal, grp chan<- map[string]Group, mute chan<- bool) {

	defer wg.Done()
	<-kill
	close(mute)
	close(grp)
	close(kill)
	close(hup)
	log <- []string{"Broadcaster exited successfully.\n"}
	//close(log)
}

// jobWorker listens on the jobs channel and handles
// any incoming job.
func jobWorker(jobs <-chan *Job, retries int) {

	var (
		statusCode int
		err        error
	)

	defer wg.Done()
	for job := range jobs {
		for i := 0; i <= retries; i++ {
			client := job.Cache.Client
			reqString := job.Cache.Address + job.Cache.Item
			r, err := http.NewRequest(job.Cache.Method, reqString, nil)
			if err != nil {
				statusCode = http.StatusInternalServerError
				job.Cache.Client = createHTTPClient()
				continue
			}
			// Preserve the headers
			for k, v := range job.Cache.Headers {
				r.Header.Set(k, strings.Join(v, " "))
			}
			// The "Host" header is the hardest
			r.Header.Set("X-Host", job.Cache.Headers.Get("Host"))
			r.Host = job.Cache.Headers.Get("Host")
			resp, err := client.Do(r)
			if err != nil {
				statusCode = http.StatusInternalServerError
				job.Cache.Client = createHTTPClient()
				continue
			}
			_, err = io.Copy(io.Discard, resp.Body)
			if err != nil {
				statusCode = http.StatusInternalServerError
				job.Cache.Client = createHTTPClient()
				continue
			}
			resp.Body.Close()
			statusCode = resp.StatusCode
			break
		}
		if err != nil {
			job.Result <- []byte(err.Error())
			continue
		}
		job.Status <- statusCode
	}
}
