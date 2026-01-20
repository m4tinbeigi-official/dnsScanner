#!/usr/bin/env node
"use strict"

const fs = require("fs")
const path = require("path")
const net = require("net")
const http = require("http")

const BASE_DIR = process.cwd()
const CONFIG_DIR = path.join(BASE_DIR, "config")
const RUNTIME_DIR = path.join(BASE_DIR, "runtime")
const RESULT_DIR = path.join(BASE_DIR, "result")

const DNS_DB = path.join(RESULT_DIR, "dns.json")
const ACTIVE_POOL = path.join(RUNTIME_DIR, "active.json")
const CLIENT_DB = path.join(RUNTIME_DIR, "clients.json")
const DOMAINS_FILE = path.join(CONFIG_DIR, "domains.conf")
const CLIENTS_FILE = path.join(CONFIG_DIR, "clients.conf")

const MAX_LATENCY = 800
const ROTATE_INTERVAL = 10000
const API_PORT = 9180
const WEB_PORT = 9181

function ensureDir(d) {
  if (!fs.existsSync(d)) fs.mkdirSync(d, { recursive: true })
}

function ensureFile(f, content) {
  if (!fs.existsSync(f)) fs.writeFileSync(f, content)
}

ensureDir(CONFIG_DIR)
ensureDir(RUNTIME_DIR)
ensureDir(RESULT_DIR)

ensureFile(
  DNS_DB,
  JSON.stringify(
    [
      { ip: "1.1.1.1", latency: 40, ipv6: false, anycast: true },
      { ip: "8.8.8.8", latency: 55, ipv6: false, anycast: true },
      { ip: "2606:4700:4700::1111", latency: 45, ipv6: true, anycast: true }
    ],
    null,
    2
  )
)

ensureFile(DOMAINS_FILE, "example.com\ngoogle.com\ncloudflare.com\n")
ensureFile(CLIENTS_FILE, "0.0.0.0/0 DEFAULT\n")
ensureFile(CLIENT_DB, JSON.stringify([], null, 2))
ensureFile(ACTIVE_POOL, JSON.stringify([], null, 2))

function loadJSON(f) {
  return JSON.parse(fs.readFileSync(f, "utf8"))
}

function saveJSON(f, d) {
  fs.writeFileSync(f, JSON.stringify(d, null, 2))
}

function selectActivePool() {
  const db = loadJSON(DNS_DB)
  return db.filter(d => d.latency <= MAX_LATENCY)
           .sort((a, b) => a.latency - b.latency)
}

function rotate(arr) {
  if (arr.length <= 1) return arr
  return [arr[arr.length - 1], ...arr.slice(0, arr.length - 1)]
}

function updatePool() {
  let pool = selectActivePool()
  pool = rotate(pool)
  saveJSON(ACTIVE_POOL, pool)
}

function autoDetectClient(ip) {
  const clients = loadJSON(CLIENT_DB)
  if (!clients.includes(ip)) {
    clients.push(ip)
    saveJSON(CLIENT_DB, clients)
  }
}

setInterval(updatePool, ROTATE_INTERVAL)
updatePool()

const apiServer = http.createServer((req, res) => {
  if (req.url === "/status") {
    const pool = loadJSON(ACTIVE_POOL)
    const clients = loadJSON(CLIENT_DB)
    res.writeHead(200, { "Content-Type": "application/json" })
    res.end(JSON.stringify({ pool, clients }))
    return
  }
  res.writeHead(404)
  res.end()
})

apiServer.listen(API_PORT)

const webServer = http.createServer((req, res) => {
  const pool = loadJSON(ACTIVE_POOL)
  const clients = loadJSON(CLIENT_DB)

  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(`
    <html>
    <body>
      <h2>DNS Orchestrator</h2>
      <h3>Active DNS</h3>
      <pre>${JSON.stringify(pool, null, 2)}</pre>
      <h3>Clients</h3>
      <pre>${JSON.stringify(clients, null, 2)}</pre>
    </body>
    </html>
  `)
})

webServer.listen(WEB_PORT)

const tcpWatcher = net.createServer(socket => {
  const ip = socket.remoteAddress.replace("::ffff:", "")
  autoDetectClient(ip)
  socket.end()
})

tcpWatcher.listen(9055)

console.log("DNS Orchestrator JS started")
console.log("API on port", API_PORT)
console.log("Web panel on port", WEB_PORT)
console.log("Client detector on port 9055")