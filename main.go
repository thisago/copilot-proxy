package main

import (
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// tokenExpiryBuffer is how far before actual expiry we consider a CopilotToken expired to avoid races
const tokenExpiryBuffer = 10 * time.Second

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
}

type AccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

type CopilotToken struct {
	Token  string `json:"token"`
	Expiry int64  `json:"expires_at"`
}
type TokenCache struct {
	mu       sync.Mutex
	cache    map[string]CopilotToken
	timer    *time.Timer
	stopChan chan struct{}
}

func NewTokenCache() *TokenCache {
	tc := &TokenCache{
		cache:    make(map[string]CopilotToken),
		stopChan: make(chan struct{}),
	}
	tc.scheduleCleanup()
	return tc
}

func (tc *TokenCache) Set(key string, token CopilotToken) {
	tc.mu.Lock()
	tc.cache[key] = token
	tc.mu.Unlock()
	// schedule a cleanup taking into account our expiry buffer
	tc.scheduleCleanup()
}

func (tc *TokenCache) Get(key string) (CopilotToken, bool) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	token, ok := tc.cache[key]
	// treat tokens as expired tokenExpiryBuffer before their actual expiry to avoid races
	if !ok || time.Until(time.Unix(token.Expiry, 0)) <= tokenExpiryBuffer {
		delete(tc.cache, key)
		return CopilotToken{}, false
	}
	return token, true
}

func (tc *TokenCache) scheduleCleanup() {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.timer != nil {
		tc.timer.Stop()
	}

	var earliestEvict time.Time
	for _, v := range tc.cache {
		// schedule eviction tokenExpiryBuffer before actual expiry
		evict := time.Unix(v.Expiry, 0).Add(-tokenExpiryBuffer)
		if earliestEvict.IsZero() || evict.Before(earliestEvict) {
			earliestEvict = evict
		}
	}
	if earliestEvict.IsZero() {
		return
	}
	// compute duration until eviction time
	duration := time.Until(earliestEvict)
	if duration <= 0 {
		duration = time.Second
	}
	tc.timer = time.AfterFunc(duration, func() {
		tc.cleanup()
	})
}

func (tc *TokenCache) cleanup() {
	tc.mu.Lock()
	now := time.Now().Unix()
	for k, v := range tc.cache {
		if v.Expiry <= now {
			delete(tc.cache, k)
		}
	}
	tc.mu.Unlock()
	tc.scheduleCleanup()
}

var upgrader = websocket.Upgrader{}
var tokenCache = NewTokenCache()

func main() {
	listenAddr := "127.0.0.1:8080"
	if len(os.Args) > 1 {
		for i, arg := range os.Args {
			if arg == "-listen" && i+1 < len(os.Args) {
				listenAddr = os.Args[i+1]
			}
		}
	}
	http.Handle("/", loggingMiddleware(http.HandlerFunc(handleIndex)))
	http.Handle("/login", loggingMiddleware(http.HandlerFunc(handleLogin)))
	http.Handle("/ws/poll", http.HandlerFunc(handleWebsocketPoll))
	http.Handle("/chat/completions", loggingMiddleware(http.HandlerFunc(handleGitHubProxy)))
	http.Handle("/models", loggingMiddleware(http.HandlerFunc(handleGitHubProxy)))
	log.Printf("Listening at http://%s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
