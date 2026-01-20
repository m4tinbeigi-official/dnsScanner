use std::fs::{self, File};
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream, SocketAddr};
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
}

#[derive(Serialize, Deserialize, Clone)]
struct Client {
    ip: String,
    tag: String,
    geo: String,
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

const MAX_LATENCY: u32 = 800;
const ROTATE_INTERVAL: u64 = 10; // seconds
const TCP_PORT: u16 = 9055;
const API_PORT: u16 = 9180;
const WEB_PORT: u16 = 9181;

// Utility functions
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

fn load_dns_db() -> Vec<DNS> {
    let mut data = String::new();
    File::open(DNS_DB_FILE).unwrap().read_to_string(&mut data).unwrap();
    serde_json::from_str(&data).unwrap()
}

fn save_json<T: Serialize>(path: &str, v: &T) {
    let data = serde_json::to_string_pretty(v).unwrap();
    let mut f = File::create(path).unwrap();
    f.write_all(data.as_bytes()).unwrap();
}

fn select_active_pool(db: &Vec<DNS>) -> Vec<DNS> {
    let mut pool: Vec<DNS> = db.iter().cloned().filter(|d| d.latency <= MAX_LATENCY).collect();
    pool.sort_by_key(|d| d.latency);
    pool
}

fn rotate_pool(pool: &mut Vec<DNS>) {
    if pool.len() > 1 {
        let last = pool.pop().unwrap();
        pool.insert(0, last);
    }
}

// TCP Client Watcher
fn tcp_watcher(clients: Arc<Mutex<Vec<Client>>>) {
    let listener = TcpListener::bind(("0.0.0.0", TCP_PORT)).unwrap();
    for stream in listener.incoming() {
        if let Ok(s) = stream {
            let ip = s.peer_addr().unwrap().ip().to_string();
            let mut clients_lock = clients.lock().unwrap();
            if !clients_lock.iter().any(|c| c.ip == ip) {
                clients_lock.push(Client { ip: ip.clone(), tag: "DEFAULT".to_string(), geo: "DEFAULT".to_string() });
                save_json(CLIENT_DB_FILE, &*clients_lock);
            }
        }
    }
}

#[tokio::main]
async fn main() {
    // Directories and files
    ensure_dir(CONFIG_DIR);
    ensure_dir(RUNTIME_DIR);
    ensure_dir(RESULT_DIR);
    ensure_file(DNS_DB_FILE, r#"[
        {"ip":"1.1.1.1","latency":40,"ipv6":false,"anycast":true,"region":"DEFAULT"},
        {"ip":"8.8.8.8","latency":55,"ipv6":false,"anycast":true,"region":"DEFAULT"},
        {"ip":"2606:4700:4700::1111","latency":45,"ipv6":true,"anycast":true,"region":"DEFAULT"}
    ]"#);
    ensure_file(CLIENT_DB_FILE, "[]");

    let clients = Arc::new(Mutex::new(Vec::<Client>::new()));

    // TCP Watcher thread
    let clients_clone = Arc::clone(&clients);
    thread::spawn(move || tcp_watcher(clients_clone));

    // Rotation thread
    let active_pool = Arc::new(Mutex::new(Vec::<DNS>::new()));
    let active_clone = Arc::clone(&active_pool);
    thread::spawn(move || {
        loop {
            let db = load_dns_db();
            let mut pool = select_active_pool(&db);
            rotate_pool(&mut pool);
            save_json(ACTIVE_POOL_FILE, &pool);
            let mut lock = active_clone.lock().unwrap();
            *lock = pool;
            thread::sleep(Duration::from_secs(ROTATE_INTERVAL));
        }
    });

    // API server
    let api_clients = Arc::clone(&clients);
    let api_pool = Arc::clone(&active_pool);
    let api_route = warp::path("api").map(move || {
        let pool = api_pool.lock().unwrap();
        let cls = api_clients.lock().unwrap();
        warp::reply::json(&serde_json::json!({
            "dns_pool": *pool,
            "clients": *cls
        }))
    });
    warp::serve(api_route).run(([0,0,0,0], API_PORT)).await;
}