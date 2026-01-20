<?php
declare(strict_types=1);

// ------------------------------
// Paths and config
// ------------------------------
$baseDir      = __DIR__;
$configDir    = "$baseDir/config";
$resultDir    = "$baseDir/result";
$runtimeDir   = "$baseDir/runtime";

$dnsFile      = "$resultDir/dns.json";
$activeFile   = "$runtimeDir/active.json";
$clientFile   = "$runtimeDir/clients.json";
$domainsFile  = "$configDir/domains.conf";
$geoFile      = "$configDir/geo.conf";
$logFile      = "$resultDir/dns.log";

$maxLatency     = 800;
$rotateInterval = 10; // seconds

// ------------------------------
// Utility functions
// ------------------------------
function ensureDir(string $dir) {
    if (!is_dir($dir)) mkdir($dir, 0755, true);
}

function ensureFile(string $file, string $default) {
    if (!file_exists($file)) file_put_contents($file, $default);
}

function loadJSON(string $file) {
    if (!file_exists($file)) return [];
    $content = file_get_contents($file);
    $data = json_decode($content, true);
    return $data ?: [];
}

function saveJSON(string $file, $data) {
    file_put_contents($file, json_encode($data, JSON_PRETTY_PRINT));
}

function logMessage(string $msg) {
    global $logFile;
    $time = date("Y-m-d H:i:s");
    file_put_contents($logFile, "[$time] $msg\n", FILE_APPEND);
}

// ------------------------------
// DNS Pool Management
// ------------------------------
function selectActivePool(array $db) : array {
    global $maxLatency;
    $pool = array_filter($db, fn($d)=> $d['latency'] <= $maxLatency);
    usort($pool, fn($a,$b)=> $a['latency'] <=> $b['latency']);
    return $pool;
}

function rotatePool(array &$pool) {
    if(count($pool) > 1) {
        $last = array_pop($pool);
        array_unshift($pool, $last);
    }
}

// ------------------------------
// Client detection / Geo
// ------------------------------
function autoDetectClient(string $ip, array $domains) {
    global $clientFile;
    $clients = loadJSON($clientFile);
    foreach($clients as $c) {
        if($c['ip'] === $ip) return;
    }
    $clients[] = [
        'ip' => $ip,
        'tag'=> 'DEFAULT',
        'geo'=> 'DEFAULT',
        'domains' => $domains
    ];
    saveJSON($clientFile, $clients);
    logMessage("New client detected: $ip");
}

// ------------------------------
// Web API
// ------------------------------
function renderWebPanel() {
    global $activeFile, $clientFile, $logFile;

    $pool = loadJSON($activeFile);
    $clients = loadJSON($clientFile);
    $logs = file_exists($logFile) ? file_get_contents($logFile) : "";

    echo "<html><head><title>DNS Orchestrator</title></head><body>";
    echo "<h2>Active DNS Pool</h2><pre>".htmlspecialchars(json_encode($pool, JSON_PRETTY_PRINT))."</pre>";
    echo "<h2>Clients</h2><pre>".htmlspecialchars(json_encode($clients, JSON_PRETTY_PRINT))."</pre>";
    echo "<h2>Logs</h2><pre>".htmlspecialchars($logs)."</pre>";
    echo "</body></html>";
}

// ------------------------------
// Main
// ------------------------------
ensureDir($configDir);
ensureDir($runtimeDir);
ensureDir($resultDir);

ensureFile($dnsFile, json_encode([
    ["ip"=>"1.1.1.1","latency"=>40,"ipv6"=>false,"anycast"=>true,"region"=>"DEFAULT","domains"=>["example.com"]],
    ["ip"=>"8.8.8.8","latency"=>55,"ipv6"=>false,"anycast"=>true,"region"=>"DEFAULT","domains"=>["google.com"]],
    ["ip"=>"2606:4700:4700::1111","latency"=>45,"ipv6"=>true,"anycast"=>true,"region"=>"DEFAULT","domains"=>["cloudflare.com"]]
], JSON_PRETTY_PRINT));

ensureFile($activeFile, "[]");
ensureFile($clientFile, "[]");
ensureFile($domainsFile, "example.com\ngoogle.com\ncloudflare.com");
ensureFile($geoFile, "DEFAULT 0.0.0.0/0");
ensureFile($logFile, "");

// Load domains
$domains = array_filter(array_map('trim', file($domainsFile)));

// Pool rotation / health loop
$poolThread = function() use ($dnsFile, $activeFile) {
    global $rotateInterval;
    while(true) {
        $db = loadJSON($dnsFile);
        $pool = selectActivePool($db);
        rotatePool($pool);
        saveJSON($activeFile, $pool);
        logMessage("DNS pool updated.");
        sleep($rotateInterval);
    }
};

// Start pool rotation in background
if (function_exists('pcntl_fork')) {
    $pid = pcntl_fork();
    if ($pid == 0) $poolThread();
}

// Simulate TCP client detection (for demo, just a random IP)
$clientIP = "192.168.1." . rand(2,254);
autoDetectClient($clientIP, $domains);

// Render web panel
renderWebPanel();