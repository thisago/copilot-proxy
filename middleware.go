package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var logMutex sync.Mutex

type LogEntry struct {
	Timestamp      string      `json:"timestamp"`
	Method         string      `json:"method"`
	URL            string      `json:"url"`
	Headers        http.Header `json:"headers"`
	Body           interface{} `json:"body"`
	ResponseStatus int         `json:"response_status"`
	ResponseBody   interface{} `json:"response_body"`
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture request details
		var requestBody interface{}
		if r.Body != nil {
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore body for further use
			json.Unmarshal(bodyBytes, &requestBody)          // Attempt to parse JSON body
		}

		// Capture response details using a response recorder
		recorder := &responseRecorder{ResponseWriter: w, body: &bytes.Buffer{}}
		start := time.Now()
		next.ServeHTTP(recorder, r)
		duration := time.Since(start)

		// Attempt to parse response body as JSON
		var responseBody interface{}
		json.Unmarshal(recorder.body.Bytes(), &responseBody)

		// Create log entry
		logEntry := LogEntry{
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
			Method:         r.Method,
			URL:            r.URL.String(),
			Headers:        r.Header,
			Body:           requestBody,
			ResponseStatus: recorder.status,
			ResponseBody:   responseBody,
		}

		// Write log entry to file
		writeLogToFile(logEntry)
		log.Printf("Request processed in %s\n", duration)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	body   *bytes.Buffer
}

func (rec *responseRecorder) WriteHeader(statusCode int) {
	rec.status = statusCode
	rec.ResponseWriter.WriteHeader(statusCode)
}

func (rec *responseRecorder) Write(data []byte) (int, error) {
	rec.body.Write(data)
	return rec.ResponseWriter.Write(data)
}

func writeLogToFile(entry LogEntry) {
	logMutex.Lock()
	defer logMutex.Unlock()

	// Ensure logs directory exists
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Failed to create logs directory: %v", err)
		return
	}

	// Open the daily log file
	logFile := filepath.Join(logDir, time.Now().UTC().Format("2006-01-02")+".json")
	file, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		return
	}
	defer file.Close()

	// Read existing logs (if any)
	var logs []LogEntry
	stat, err := file.Stat()
	if err != nil {
		log.Printf("Failed to get file stats: %v", err)
		return
	}
	if stat.Size() > 0 {
		// Reset file pointer to the beginning for reading
		if _, err := file.Seek(0, 0); err != nil {
			log.Printf("Failed to seek to the beginning of the file: %v", err)
			return
		}
		if err := json.NewDecoder(file).Decode(&logs); err != nil {
			log.Printf("Failed to decode existing logs: %v", err)
			return
		}
	}

	// Append the new log entry
	logs = append(logs, entry)

	// Truncate the file and write back the updated logs
	if err := file.Truncate(0); err != nil {
		log.Printf("Failed to truncate log file: %v", err)
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		log.Printf("Failed to seek to the beginning of the file: %v", err)
		return
	}
	if err := json.NewEncoder(file).Encode(logs); err != nil {
		log.Printf("Failed to write log entry: %v", err)
	}
}
