// pm2 process file of the staging VM (deploy/vm/README.md, developer-spec.md §17.4), started by
// rollout.sh as the logitrack user: `pm2 startOrReload /opt/logitrack/current/deploy/vm/ecosystem.config.cjs`.
//
// Every app runs through run-app.sh, which reads /opt/logitrack/shared/.env at exec time and exports only
// that program's names (env-names/<program>.txt), so no value of the .env ever lands in pm2's process
// list or dump (`pm2 save`). The `env` blocks below hold only the per-process listener settings (not
// secrets): the ports differ per app, so they cannot live in the shared .env. The web gets only web-server
// names; the Go apps never get one.
"use strict";

const fs = require("node:fs");
const path = require("node:path");

/** kit.conf (kit_name=value lines): the one place for the user, the tree and the ports. */
function readKit(file) {
    const kit = {};
    for (const line of fs.readFileSync(file, "utf8").split("\n")) {
        const m = /^(kit_[a-z_]+)=(.*)$/.exec(line.trim());
        if (m) kit[m[1]] = m[2];
    }
    return kit;
}

const kit = readKit(path.join(__dirname, "kit.conf"));
const current = `${kit.kit_root}/current`;
const logs = `${kit.kit_root}/shared/logs`;
const loopback = (port) => `127.0.0.1:${port}`;

/** One pm2 app started through the wrapper; `program` is a binary of bin/ or "web". */
function app(name, program, env, extra) {
    return {
        name,
        script: `${current}/deploy/vm/run-app.sh`,
        args: [program],
        interpreter: "/bin/bash",
        cwd: kit.kit_root,
        exec_mode: "fork",
        instances: 1,
        autorestart: true,
        max_restarts: 20,
        min_uptime: "10s",
        exp_backoff_restart_delay: 200,
        out_file: `${logs}/${program}.out.log`,
        error_file: `${logs}/${program}.err.log`,
        merge_logs: true,
        env,
        ...extra,
    };
}

// The Go processes drain for SHUTDOWN_TIMEOUT (30 s) after SIGINT; pm2 waits a little longer before SIGKILL.
const go = { kill_timeout: 40000 };

module.exports = {
    apps: [
        app("logitrack-api", "api", {
            // Both listeners on loopback: nginx proxies only to the public one; the BFF calls the internal one.
            API_INTERNAL_ADDR: loopback(kit.kit_api_internal_port),
            API_PUBLIC_ADDR: loopback(kit.kit_api_public_port),
            METRICS_ADDR: loopback(kit.kit_api_metrics_port),
        }, go),
        app("logitrack-worker", "worker", {
            METRICS_ADDR: loopback(kit.kit_worker_metrics_port),
        }, go),
        app("logitrack-scheduler", "scheduler", {
            METRICS_ADDR: loopback(kit.kit_scheduler_metrics_port),
        }, go),
        app("logitrack-web", "web", {
            // §16.1 web-server names only. The BFF's upstream is the api's internal listener.
            GO_API_INTERNAL_URL: `http://${loopback(kit.kit_api_internal_port)}`,
            PORT: kit.kit_web_port,
            HOSTNAME: "127.0.0.1",
            NODE_ENV: "production",
        }, { kill_timeout: 10000 }),
    ],
};
