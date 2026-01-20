<?php
declare(strict_types=1);

// ------------------------------
// Paths & Config
// ------------------------------
$baseDir      = __DIR__;
$configDir    = "$baseDir/config";
$resultDir    = "$baseDir/result";
$runtimeDir   = "$baseDir/runtime";

$dnsFile      = "$resultDir/dns.json";
$activeFile   = "$runtimeDir/active.json";
$clientFile   = "$runtimeDir/clients.json";
$domainsFile  = "$configDir/domains.conf";
$logFile      = "$resultDir/dns.log";

$rotateInterval = 10; // seconds
$dnsTimeout     = 2;  // seconds for dig/nslookup
$maxLatency     = 800; // ms threshold for healthy

// ------------------------------
// Self-Init
// ------------------------------
function ensureDir(string $dir) { if(!is_dir($dir)) mkdir($dir, 0755, true); }
function ensureFile(string $file, string $default) { if(!file_exists($file)) file_put_contents($file, $default); }

ensureDir($configDir); ensureDir($runtimeDir); ensureDir($resultDir);
ensureFile($dnsFile, json_encode([
    ["ip"=>"1.1.1.1","ipv6"=>false,"anycast"=>true,"region"=>"DEFAULT","domains"=>["example.com"]],
    ["ip"=>"8.8.8.8","ipv6"=>false,"anycast"=>true,"region"=>"DEFAULT","domains"=>["google.com"]],
    ["ip"=>"2606:4700:4700::1111","ipv6"=>true,"anycast"=>true,"region"=>"DEFAULT","domains"=>["cloudflare.com"]]
], JSON_PRETTY_PRINT));
ensureFile($activeFile, "[]"); 
ensureFile($clientFile, "[]");
ensureFile($domainsFile, "example.com\ngoogle.com\ncloudflare.com"); 
ensureFile($logFile, "");

// ------------------------------
// Utils
// ------------------------------
function loadJSON(string $file) { return json_decode(file_get_contents($file), true) ?? []; }
function saveJSON(string $file, $data) { file_put_contents($file, json_encode($data, JSON_PRETTY_PRINT)); }
function logMessage(string $msg) { global $logFile; $time=date("Y-m-d H:i:s"); file_put_contents($logFile,"[$time] $msg\n", FILE_APPEND); }

// ------------------------------
// Client Detection
// ------------------------------
function autoDetectClient(string $ip, array $domains) {
    global $clientFile;
    $clients = loadJSON($clientFile);
    foreach($clients as $c) if($c['ip']==$ip) return;
    $clients[] = ['ip'=>$ip,'tag'=>'DEFAULT','geo'=>'DEFAULT','domains'=>$domains];
    saveJSON($clientFile,$clients);
    logMessage("New client detected: $ip");
}

// ------------------------------
// DNS Health Check
// ------------------------------
function dnsQueryLatency(string $dns, string $domain, int $timeout=2): ?float {
    $start = microtime(true);
    $cmd = filter_var($dns, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) ? 
        "dig -6 @$dns $domain +time=$timeout +tries=1 +short" : 
        "dig @$dns $domain +time=$timeout +tries=1 +short";
    exec($cmd,$output,$ret);
    if($ret===0 && !empty($output)) {
        return (microtime(true)-$start)*1000; // latency in ms
    }
    return null;
}

function checkDNSPool(array $pool, array $domains): array {
    foreach($pool as &$dns) {
        $latencies=[];
        foreach($dns['domains'] as $domain) {
            $lat = dnsQueryLatency($dns['ip'],$domain);
            if($lat!==null) $latencies[]=$lat;
        }
        $dns['latency'] = empty($latencies)?999:array_sum($latencies)/count($latencies);
        $dns['healthy'] = $dns['latency']<$GLOBALS['maxLatency'];
    }
    unset($dns);
    usort($pool, fn($a,$b)=>$a['latency']<=>$b['latency']);
    return $pool;
}

// ------------------------------
// AJAX Polling Handler
// ------------------------------
if(isset($_GET['ajax'])) {
    $domains = array_filter(array_map('trim', file($domainsFile)));
    autoDetectClient("192.168.1.".rand(2,254), $domains); // simulate client

    $pool = loadJSON($dnsFile);
    $pool = checkDNSPool($pool,$domains);
    saveJSON($activeFile,$pool);

    $data = [
        'pool'=>$pool,
        'clients'=>loadJSON($clientFile),
        'log'=>file_get_contents($logFile)
    ];
    header('Content-Type: application/json');
    echo json_encode($data);
    exit;
}

// ------------------------------
// Web Panel HTML + JS
// ------------------------------
?>
<!DOCTYPE html>
<html>
<head>
<title>DNS Orchestrator PRO</title>
<style>
body {font-family: monospace; background:#111;color:#eee;}
pre {background:#222;padding:10px;border-radius:5px;overflow:auto; max-height:300px;}
.healthy {color:#0f0;}
.unhealthy {color:#f00;}
</style>
</head>
<body>
<h2>Active DNS Pool</h2>
<pre id="pool">Loading...</pre>

<h2>Clients</h2>
<pre id="clients">Loading...</pre>

<h2>Logs</h2>
<pre id="log">Loading...</pre>

<script>
function fetchData(){
    fetch('?ajax=1').then(r=>r.json()).then(data=>{
        const poolHTML = data.pool.map(d=>`${d.ip} (${d.latency.toFixed(1)}ms) ${d.healthy?'HEALTHY':'UNHEALTHY'}`).join("\n");
        document.getElementById('pool').textContent = poolHTML;
        document.getElementById('clients').textContent = JSON.stringify(data.clients,null,2);
        document.getElementById('log').textContent = data.log;
    });
}
setInterval(fetchData, 5000);
fetchData();
</script>
</body>
</html>