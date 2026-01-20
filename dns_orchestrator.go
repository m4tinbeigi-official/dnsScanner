package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ------------------------------
// Paths and constants
// ------------------------------
var (
	baseDir      = "."
	configDir    = filepath.Join(baseDir, "config")
	runtimeDir   = filepath.Join(baseDir, "runtime")
	resultDir    = filepath.Join(baseDir, "result")
	dnsDBFile    = filepath.Join(resultDir, "dns.json")
	activePool   = filepath.Join(runtimeDir, "active.json")
	clientDBFile = filepath.Join(runtimeDir, "clients.json")
	domainsFile  = filepath.Join(configDir, "domains.conf")
	clientsFile  = filepath.Join(configDir, "clients.conf")
	geoFile      = filepath.Join(configDir, "geo.conf")

	maxLatency     = 800
	rotateInterval = 10 * time.Second
	apiPort        = 9180
	webPort        = 9181
	tcpPort        = 9055

	mu sync.Mutex
)

// ------------------------------
// Types
// ------------------------------
type DNS struct {
	IP      string `json:"ip"`
	Latency int    `json:"latency"`
	IPv6    bool   `json:"ipv6"`
	Anycast bool   `json:"anycast"`
	Region  string `json:"region,omitempty"`
}

type Client struct {
	IP   string `json:"ip"`
	Tag  string `json:"tag"`
	Geo  string `json:"geo"`
}

// ------------------------------
// Utility functions
// ------------------------------
func ensureDir(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.MkdirAll(path, 0755)
	}
}

func ensureFile(file, defaultContent string) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		ioutil.WriteFile(file, []byte(defaultContent), 0644)
	}
}

func loadJSON(file string, v interface{}) {
	data, err := ioutil.ReadFile(file)
	if err == nil {
		json.Unmarshal(data, v)
	}
}

func saveJSON(file string, v interface{}) {
	data, _ := json.MarshalIndent(v, "", "  ")
	ioutil.WriteFile(file, data, 0644)
}

// ------------------------------
// DNS Pool Management
// ------------------------------
func selectActivePool() []DNS {
	db := []DNS{}
	loadJSON(dnsDBFile, &db)
	var pool []DNS
	for _, d := range db {
		if d.Latency <= maxLatency {
			pool = append(pool, d)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Latency < pool[j].Latency })
	return pool
}

func rotatePool(pool []DNS) []DNS {
	if len(pool) <= 1 {
		return pool
	}
	return append([]DNS{pool[len(pool)-1]}, pool[:len(pool)-1]...)
}

func updatePool() {
	mu.Lock()
	defer mu.Unlock()
	pool := selectActivePool()
	pool = rotatePool(pool)
	saveJSON(activePool, pool)
}

// ------------------------------
// Client Detection and Geo
// ------------------------------
func autoDetectClient(ip string) {
	mu.Lock()
	defer mu.Unlock()

	clients := []Client{}
	loadJSON(clientDBFile, &clients)
	for _, c := range clients {
		if c.IP == ip {
			return
		}
	}

	tag := detectClientTag(ip)
	geo := detectGeo(ip)

	c := Client{IP: ip, Tag: tag, Geo: geo}
	clients = append(clients, c)
	saveJSON(clientDBFile, clients)
}

func detectClientTag(ip string) string {
	lines, _ := ioutil.ReadFile(clientsFile)
	for _, line := range strings.Split(string(lines), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 {
			subnet := parts[0]
			tag := parts[1]
			if subnet == "0.0.0.0/0" {
				return tag
			}
			if ipInSubnet(ip, subnet) {
				return tag
			}
		}
	}
	return "DEFAULT"
}

func detectGeo(ip string) string {
	lines, _ := ioutil.ReadFile(geoFile)
	for _, line := range strings.Split(string(lines), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 {
			region := parts[0]
			subnet := parts[1]
			if subnet == "0.0.0.0/0" {
				return region
			}
			if ipInSubnet(ip, subnet) {
				return region
			}
		}
	}
	return "UNKNOWN"
}

// ------------------------------
// IP/Subnet utils
// ------------------------------
func ipInSubnet(ipStr, subnetStr string) bool {
	ip := net.ParseIP(ipStr)
	_, subnet, err := net.ParseCIDR(subnetStr)
	if err != nil || ip == nil {
		return false
	}
	return subnet.Contains(ip)
}

// ------------------------------
// API / Web
// ------------------------------
func apiHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	pool := []DNS{}
	clients := []Client{}
	loadJSON(activePool, &pool)
	loadJSON(clientDBFile, &clients)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"dns_pool": pool,
		"clients":  clients,
	})
}

func webHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	pool := []DNS{}
	clients := []Client{}
	loadJSON(activePool, &pool)
	loadJSON(clientDBFile, &clients)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<html><body><h2>DNS Orchestrator</h2><h3>Active DNS</h3><pre>%v</pre><h3>Clients</h3><pre>%v</pre></body></html>", pool, clients)
}

// ------------------------------
// TCP Client Watcher
// ------------------------------
func tcpWatcher() {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
	if err != nil {
		panic(err)
	}
	for {
		conn, _ := ln.Accept()
		ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		autoDetectClient(ip)
		conn.Close()
	}
}

// ------------------------------
// Main
// ------------------------------
func main() {
	// Initialize directories and files
	ensureDir(configDir)
	ensureDir(runtimeDir)
	ensureDir(resultDir)

	ensureFile(dnsDBFile, `[
  {"ip":"1.1.1.1","latency":40,"ipv6":false,"anycast":true,"region":"DEFAULT"},
  {"ip":"8.8.8.8","latency":55,"ipv6":false,"anycast":true,"region":"DEFAULT"},
  {"ip":"2606:4700:4700::1111","latency":45,"ipv6":true,"anycast":true,"region":"DEFAULT"}
]`)
	ensureFile(domainsFile, "example.com\ngoogle.com\ncloudflare.com\n")
	ensureFile(clientsFile, "0.0.0.0/0 DEFAULT\n")
	ensureFile(geoFile, "DEFAULT 0.0.0.0/0\n")
	ensureFile(clientDBFile, "[]")
	ensureFile(activePool, "[]")

	// Update pool periodically
	go func() {
		for {
			updatePool()
			time.Sleep(rotateInterval)
		}
	}()

	// Start API and Web servers
	http.HandleFunc("/api", apiHandler)
	go http.ListenAndServe(fmt.Sprintf(":%d", apiPort), nil)

	http.HandleFunc("/", webHandler)
	go http.ListenAndServe(fmt.Sprintf(":%d", webPort), nil)

	// TCP Client Watcher
	go tcpWatcher()

	fmt.Println("Full DNS Orchestrator Go started")
	fmt.Printf("API: %d, Web: %d, TCP client detection: %d\n", apiPort, webPort, tcpPort)

	select {} // Block forever
}