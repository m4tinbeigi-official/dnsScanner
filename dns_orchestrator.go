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
	"sync"
	"time"
)

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

	maxLatency     = 800
	rotateInterval = 10 * time.Second
	apiPort        = 9180
	webPort        = 9181

	mu sync.Mutex
)

type DNS struct {
	IP       string `json:"ip"`
	Latency  int    `json:"latency"`
	IPv6     bool   `json:"ipv6"`
	Anycast  bool   `json:"anycast"`
}

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

func loadDNSDB() []DNS {
	data, _ := ioutil.ReadFile(dnsDBFile)
	var db []DNS
	json.Unmarshal(data, &db)
	return db
}

func saveJSON(file string, v interface{}) {
	data, _ := json.MarshalIndent(v, "", "  ")
	ioutil.WriteFile(file, data, 0644)
}

func selectActivePool() []DNS {
	db := loadDNSDB()
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

func autoDetectClient(ip string) {
	mu.Lock()
	defer mu.Unlock()
	clients := []string{}
	data, _ := ioutil.ReadFile(clientDBFile)
	json.Unmarshal(data, &clients)
	for _, c := range clients {
		if c == ip {
			return
		}
	}
	clients = append(clients, ip)
	saveJSON(clientDBFile, clients)
}

func apiHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	pool := []DNS{}
	clients := []string{}
	data, _ := ioutil.ReadFile(activePool)
	json.Unmarshal(data, &pool)
	cdata, _ := ioutil.ReadFile(clientDBFile)
	json.Unmarshal(cdata, &clients)
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
	clients := []string{}
	data, _ := ioutil.ReadFile(activePool)
	json.Unmarshal(data, &pool)
	cdata, _ := ioutil.ReadFile(clientDBFile)
	json.Unmarshal(cdata, &clients)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<html><body><h2>DNS Orchestrator</h2><h3>Active DNS</h3><pre>%v</pre><h3>Clients</h3><pre>%v</pre></body></html>", pool, clients)
}

func main() {
	// ایجاد دایرکتوری‌ها و فایل‌ها
	ensureDir(configDir)
	ensureDir(runtimeDir)
	ensureDir(resultDir)

	ensureFile(dnsDBFile, `[
  {"ip":"1.1.1.1","latency":40,"ipv6":false,"anycast":true},
  {"ip":"8.8.8.8","latency":55,"ipv6":false,"anycast":true},
  {"ip":"2606:4700:4700::1111","latency":45,"ipv6":true,"anycast":true}
]`)
	ensureFile(domainsFile, "example.com\ngoogle.com\ncloudflare.com\n")
	ensureFile(clientsFile, "0.0.0.0/0 DEFAULT\n")
	ensureFile(clientDBFile, "[]")
	ensureFile(activePool, "[]")

	// بروزرسانی دوره‌ای pool
	go func() {
		for {
			updatePool()
			time.Sleep(rotateInterval)
		}
	}()

	// API JSON
	http.HandleFunc("/api", apiHandler)
	go func() {
		http.ListenAndServe(fmt.Sprintf(":%d", apiPort), nil)
	}()

	// Web Panel
	http.HandleFunc("/", webHandler)
	go func() {
		http.ListenAndServe(fmt.Sprintf(":%d", webPort), nil)
	}()

	// TCP ساده برای Client Detection
	go func() {
		ln, _ := net.Listen("tcp", ":9055")
		for {
			conn, _ := ln.Accept()
			ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
			autoDetectClient(ip)
			conn.Close()
		}
	}()

	fmt.Println("DNS Orchestrator Go started")
	fmt.Printf("API: %d, Web: %d, TCP client detection: 9055\n", apiPort, webPort)

	select {}
}