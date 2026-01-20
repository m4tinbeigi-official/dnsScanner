use std::fs::{self, File};
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;
use warp::Filter;
use serde::{Serialize, Deserialize};

#[derive(Serialize, Deserialize, Clone)]
struct DNS {
    ip: String,
    latency: u32,
    ipv6: bool,
    anycast: bool,
    region: String,
    domains: Vec<String>,
}

#[derive(Serialize, Deserialize, Clone)]
struct Client {
    ip: String,
    tag: String,
    geo: String,
    domains: Vec<String>,
}

const BASE_DIR: &str = ".";
const CONFIG_DIR: &str = "./config";
const RUNTIME_DIR: &str = "./runtime";
const RESULT_DIR: &str = "./result";

const DNS_DB_FILE: &str = "./result/dns.json";
const ACTIVE_POOL_FILE: &str = "./runtime/active.json";
const CLIENT_DB_FILE: &str = "./runtime/clients.json";
const CLIENTS_FILE: &str = "./config/clients.conf";
const GEO_FILE: &str = "./config/geo.conf";
const DOMAINS_FILE: &str = "./config/domains.conf";

const MAX_LATENCY: u32 = 800;
const ROTATE_INTERVAL: u64 = 10;
const TCP_PORT: u16 = 9055;
const API_PORT: u16 = 9180;
const WEB_PORT: u16 = 9181;

// ------------------------------
// Utility functions
// ------------------------------
fn ensure_dir(path: &str) {
    if fs::metadata(path).is_err() {
        fs::create_dir_all(path).unwrap();
    }
}

fn ensure_file(path: &str, default: &str) {
    if fs::metadata(path).is_err() {
        let mut f = File::create(path).unwrap();
        f.write_all(default.as_bytes()).unwrap();
    }
}

fn load_json<T: for<'de> Deserialize<'de>>(path: &str) -> Vec<T> {
    let mut data = String::new();
    File::open(path).unwrap().read_to_string(&mut data).unwrap();
    serde_json::from_str(&data).unwrap_or(Vec::new())
}

fn save_json<T: Serialize>(path: &str, v: &Vec<T>) {
    let data = serde_json::to_string_pretty(v).unwrap();
    let mut f = File::create(path).unwrap();
    f.write_all(data.as_bytes()).unwrap();
}

// ------------------------------
// DNS Pool Management
// ------------------------------
fn select_active_pool(db: &Vec<DNS>) -> Vec<DNS> {
    let mut pool: Vec<DNS> = db.iter()
        .cloned()
        .filter(|d| d.latency <= MAX_LATENCY)
        .collect();
    pool.sort_by_key(|d| d.latency);
    pool
}

fn rotate_pool(pool: &mut Vec<DNS>) {
    if pool.len() > 1 {
        let last = pool.pop().unwrap();
        pool.insert(0, last);
    }
}

// ------------------------------
// Client Detection & Geo Routing
// ------------------------------
fn auto_detect_client(ip: String, clients: &Arc<Mutex<Vec<Client>>>, domains: &Vec<String>) {
    let mut clients_lock = clients.lock().unwrap();
    if !clients_lock.iter().any(|c| c.ip == ip) {
        let tag = detect_client_tag(&ip);
        let geo = detect_geo(&ip);
        clients_lock.push(Client { ip: ip.clone(), tag, geo, domains: domains.clone() });
        save_json(CLIENT_DB_FILE, &*clients_lock);
    }
}

fn detect_client_tag(_ip: &str) -> String {
    // simple static for now
    "DEFAULT".to_string()
}

fn detect_geo(_ip: &str) -> String {
    // simple static for now
    "DEFAULT".to_string()
}

// ------------------------------
// TCP Client Watcher
// ------------------------------
fn tcp_watcher(clients: Arc<Mutex<Vec<Client>>>, domains: Vec<String>) {
    let listener = TcpListener::bind(("0.0.0.0", TCP_PORT)).unwrap();
    for stream in listener.incoming() {
        if let Ok(_s) = stream {
            let ip = _s.peer_addr().unwrap().ip().to_string();
            auto_detect_client(ip, &clients, &domains);
        }
    }
}

// ------------------------------
// Web & API
// ------------------------------
async fn start_webapi(active_pool: Arc<Mutex<Vec<DNS>>>, clients: Arc<Mutex<Vec<Client>>>) {
    let pool_filter = warp::any().map(move || active_pool.clone());
    let clients_filter = warp::any().map(move || clients.clone());

    let api = warp::path("api").map(move || {
        let pool = active_pool.lock().unwrap();
        let cls = clients.lock().unwrap();
        warp::reply::json(&serde_json::json!({
            "dns_pool": *pool,
            "clients": *cls
        }))
    });

    let web = warp::path::end().map(move || {
        let pool = active_pool.lock().unwrap();
        let cls = clients.lock().unwrap();
        warp::reply::html(format!(
            "<html><body><h2>DNS Orchestrator</h2><h3>Active DNS</h3><pre>{:?}</pre><h3>Clients</h3><pre>{:?}</pre></body></html>",
            *pool, *cls
        ))
    });

    let routes = api.or(web);
    warp::serve(routes).run(([0,0,0,0], WEB_PORT)).await;
}

// ------------------------------
// Main
// ------------------------------
#[tokio::main]
async fn main() {
    // Directories and files
    ensure_dir(CONFIG_DIR);
    ensure_dir(RUNTIME_DIR);
    ensure_dir(RESULT_DIR);

    ensure_file(DNS_DB_FILE, r#"[
        {"ip":"1.1.1.1","latency":40,"ipv6":false,"anycast":true,"region":"DEFAULT","domains":["example.com"]},
        {"ip":"8.8.8.8","latency":55,"ipv6":false,"anycast":true,"region":"DEFAULT","domains":["google.com"]},
        {"ip":"2606:4700:4700::1111","latency":45,"ipv6":true,"anycast":true,"region":"DEFAULT","domains":["cloudflare.com"]}
    ]"#);

    ensure_file(CLIENT_DB_FILE, "[]");
    ensure_file(DOMAINS_FILE, "example.com\ngoogle.com\ncloudflare.com\n");

    let domains_data = fs::read_to_string(DOMAINS_FILE).unwrap();
    let domains: Vec<String> = domains_data.lines().map(|s| s.to_string()).collect();

    let clients = Arc::new(Mutex::new(Vec::<Client>::new()));
    let active_pool = Arc::new(Mutex::new(Vec::<DNS>::new()));

    // TCP watcher thread
    let clients_clone = Arc::clone(&clients);
    let domains_clone = domains.clone();
    thread::spawn(move || tcp_watcher(clients_clone, domains_clone));

    // Pool updater thread
    let active_clone = Arc::clone(&active_pool);
    thread::spawn(move || {
        loop {
            let db = load_json::<DNS>(DNS_DB_FILE);
            let mut pool = select_active_pool(&db);
            rotate_pool(&mut pool);
            save_json(ACTIVE_POOL_FILE, &pool);
            let mut lock = active_clone.lock().unwrap();
            *lock = pool;
            thread::sleep(Duration::from_secs(ROTATE_INTERVAL));
        }
    });

    // Start web + API
    start_webapi(active_pool, clients).await;
}