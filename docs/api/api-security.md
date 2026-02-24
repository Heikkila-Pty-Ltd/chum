# CHUM API Security

This document describes the authentication, authorization, and audit logging features for CHUM API endpoints.

## Overview

CHUM provides a lightweight HTTP API for monitoring and controlling the scheduler. Security controls protect control endpoints that can modify system state while leaving read-only monitoring endpoints accessible.

## Security Model

### Endpoint Classification

**Read-only endpoints** (no authentication required):
- `GET /status` - System status and uptime
- `GET /health` - Health check status  
- `GET /metrics` - Prometheus metrics
- `GET /projects` - Project configuration
- `GET /projects/{id}` - Project details
- `GET /teams` - Team information
- `GET /teams/{project}` - Project team details
- `GET /dispatches/{morsel_id}` - Dispatch history (read-only)
- `GET /scheduler/status` - Scheduler status
- `GET /recommendations` - System recommendations

**Control endpoints** (authentication required):
- `POST /scheduler/pause` - Pause the scheduler
- `POST /scheduler/resume` - Resume the scheduler
- `POST /dispatches/{id}/cancel` - Cancel running dispatch
- `POST /dispatches/{id}/retry` - Retry failed dispatch

## Configuration

Add security configuration to your `chum.toml`:

```toml
[api]
bind = "0.0.0.0:8080"

[api.security]
# Enable authentication for control endpoints
enabled = true

# Valid API tokens (minimum 16 characters each)
allowed_tokens = [
    "your-secure-token-here-1234567890",
    "another-token-for-different-client"
]

# Require local connections when auth is disabled (default: true for non-local bind)
require_local_only = false

# Path for audit log (optional)
audit_log = "/var/log/chum/api-audit.log"
```

### Security Modes

1. **Authentication Disabled + Local Only** (default for non-local bind):
   ```toml
   [api.security]
   enabled = false
   require_local_only = true
   ```
   - Control endpoints only accept local connections (127.0.0.1, ::1, private IPs)
   - Suitable for single-host deployments

2. **Authentication Disabled + Open** (only for localhost bind):
   ```toml
   [api.security]
   enabled = false
   require_local_only = false
   ```
   - No authentication required
   - Only safe when binding to localhost

3. **Token Authentication** (recommended for remote access):
   ```toml
   [api.security]
   enabled = true
   allowed_tokens = ["secure-token-123..."]
   audit_log = "/var/log/chum/audit.log"
   ```
   - Bearer token authentication required for control endpoints
   - Audit logging of all control operations

## API Authentication

When authentication is enabled, control endpoints require a valid Bearer token:

```bash
# Pause scheduler
curl -X POST \
  -H "Authorization: Bearer your-secure-token-here-1234567890" \
  http://chum-api:8080/scheduler/pause

# Cancel dispatch
curl -X POST \
  -H "Authorization: Bearer your-secure-token-here-1234567890" \
  http://chum-api:8080/dispatches/1234/cancel
```

Invalid or missing tokens return `401 Unauthorized`:

```json
{
  "error": "Unauthorized: valid token required"
}
```

## Audit Logging

When `audit_log` is configured, all control endpoint requests are logged in JSON format:

```json
{
  "timestamp": "2026-02-18T10:30:00Z",
  "remote_addr": "192.168.1.100:54321",
  "method": "POST",
  "path": "/scheduler/pause",
  "user_agent": "curl/7.68.0",
  "authorized": true,
  "token": "your-****",
  "status_code": 200,
  "duration": "15.2ms"
}
```

Fields:
- `timestamp` - Request timestamp (ISO 8601)
- `remote_addr` - Client IP and port
- `method` - HTTP method
- `path` - Request path
- `user_agent` - Client user agent (if provided)
- `authorized` - Whether request was authorized
- `token` - Truncated token (first 4 chars + "****")
- `error` - Error message (if request failed)
- `status_code` - HTTP response code
- `duration` - Request processing time

## Deployment Patterns

### Local Development

```toml
[api]
bind = "127.0.0.1:8080"

[api.security]
enabled = false
```

Safe for local development - only localhost connections accepted.

### Single Host Production

```toml
[api]
bind = "0.0.0.0:8080"

[api.security]
enabled = false
require_local_only = true
audit_log = "/var/log/chum/audit.log"
```

Accept connections from local network but reject external requests.

### Multi-Host Production

```toml
[api]
bind = "0.0.0.0:8080"

[api.security]
enabled = true
allowed_tokens = [
    "prod-monitoring-token-abcd1234567890",
    "ops-control-token-xyz9876543210"
]
audit_log = "/var/log/chum/audit.log"
```

Full authentication with audit logging for production environments.

### Reverse Proxy with TLS

CHUM binds to localhost and delegates TLS termination to a reverse proxy.

```nginx
# /etc/nginx/sites-available/chum-api

# Rate limiting zone: 10 req/s per IP for control endpoints
limit_req_zone $binary_remote_addr zone=chum_control:10m rate=10r/s;

server {
    listen 80;
    server_name chum-api.internal;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name chum-api.internal;

    # --- TLS ---
    ssl_certificate     /etc/ssl/certs/chum-api.pem;
    ssl_certificate_key /etc/ssl/private/chum-api.key;
    ssl_protocols       TLSv1.3;
    ssl_prefer_server_ciphers off;
    ssl_session_timeout 1d;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_tickets off;

    # OCSP stapling
    ssl_stapling on;
    ssl_stapling_verify on;
    ssl_trusted_certificate /etc/ssl/certs/ca-chain.pem;
    resolver 1.1.1.1 8.8.8.8 valid=300s;
    resolver_timeout 5s;

    # --- Security Headers ---
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options    "nosniff" always;
    add_header X-Frame-Options           "DENY" always;
    add_header Referrer-Policy           "no-referrer" always;
    add_header Content-Security-Policy   "default-src 'none'; frame-ancestors 'none'" always;

    # --- Read-only endpoints (no rate limit) ---
    location ~ ^/(status|health|metrics|projects|teams|dispatches|recommendations|scheduler/status) {
        proxy_pass http://127.0.0.1:8900;
        proxy_set_header X-Real-IP       $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Host            $host;
        proxy_read_timeout  10s;
        proxy_send_timeout  10s;
    }

    # --- Control endpoints (rate limited) ---
    location ~ ^/(scheduler/(pause|resume|plan)|dispatches/\d+/(cancel|retry)) {
        limit_req zone=chum_control burst=5 nodelay;
        limit_req_status 429;

        proxy_pass http://127.0.0.1:8900;
        proxy_set_header X-Real-IP       $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Host            $host;
        proxy_read_timeout  30s;
        proxy_send_timeout  30s;
    }

    # --- Health check for load balancers ---
    location = /healthz {
        proxy_pass http://127.0.0.1:8900/health;
        access_log off;
    }

    # Deny everything else
    location / {
        return 404;
    }
}
```

> **Note:** Adapt `server_name`, certificate paths, and port to your environment. If running behind a cloud load balancer (ALB, GCE LB), TLS may terminate at the LB and this block simplifies to plain HTTP proxy.

## Token Management

### Generation

Generate secure tokens with sufficient entropy:

```bash
# Option 1: OpenSSL
openssl rand -base64 32

# Option 2: Python
python3 -c "import secrets; print(secrets.token_urlsafe(32))"

# Option 3: Node.js
node -e "console.log(require('crypto').randomBytes(32).toString('base64url'))"
```

### Rotation

To rotate tokens:

1. Add new token to `allowed_tokens` array
2. Reload configuration: `systemctl reload chum`
3. Update clients to use new token
4. Remove old token from configuration
5. Reload configuration again

### Storage

Store tokens securely:
- Use configuration management (Ansible, Chef, etc.)
- Mount secrets as files and reference them
- Use environment variables for containerized deployments

## Monitoring

### Audit Log Queries

The audit log is structured JSON (one object per line), queryable with `jq`:

```bash
# Count failed auth attempts in last hour
jq -r 'select(.authorized == false) | .timestamp' \
  /var/log/chum/api-audit.log | \
  awk -v cutoff="$(date -u -d '1 hour ago' '+%Y-%m-%dT%H:%M')" '$0 >= cutoff' | wc -l

# List distinct source IPs hitting control endpoints (last 24h)
jq -r 'select(.method == "POST") | .remote_addr' \
  /var/log/chum/api-audit.log | \
  cut -d: -f1 | sort -u

# Show all failed requests with full context
jq 'select(.authorized == false)' /var/log/chum/api-audit.log | tail -20

# Response time percentiles for control endpoints
jq -r 'select(.method == "POST") | .duration' \
  /var/log/chum/api-audit.log | \
  sed 's/ms//' | sort -n | awk '{a[NR]=$1} END {
    print "p50:", a[int(NR*0.5)], "ms";
    print "p95:", a[int(NR*0.95)], "ms";
    print "p99:", a[int(NR*0.99)], "ms"
  }'
```

### Prometheus Metrics

> [!NOTE]
> Prometheus metrics are **planned but not yet implemented**. The metric names below are the target schema. Today, monitoring relies on audit log queries (above) and the `/health` + `/status` JSON endpoints.

Target metrics (once instrumented):

| Metric | Type | Description |
|--------|------|-------------|
| `chum_api_requests_total` | Counter | Total API requests by method, path, status |
| `chum_api_auth_failures_total` | Counter | Authentication failures by remote_addr |
| `chum_api_request_duration_seconds` | Histogram | Request latency by endpoint |
| `chum_dispatches_total` | Counter | Dispatches by project, status |
| `chum_scheduler_status` | Gauge | Current scheduler state (1=running, 0=paused) |

### Alerting Rules

Ready-to-use Prometheus alerting rules for when metrics are instrumented:

```yaml
# prometheus/rules/chum-security.yml
groups:
  - name: chum-api-security
    rules:
      - alert: CHUMHighAuthFailureRate
        expr: rate(chum_api_auth_failures_total[5m]) > 0.5
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "High authentication failure rate on CHUM API"
          description: "{{ $value | printf \"%.1f\" }} auth failures/sec over last 5 minutes"

      - alert: CHUMBruteForceDetected
        expr: increase(chum_api_auth_failures_total[15m]) > 50
        labels:
          severity: critical
        annotations:
          summary: "Possible brute-force attack on CHUM API"
          description: "{{ $value }} failed auth attempts in 15 minutes"

      - alert: CHUMUnauthorizedControlAccess
        expr: increase(chum_api_requests_total{method="POST", status="401"}[5m]) > 10
        labels:
          severity: critical
        annotations:
          summary: "Repeated unauthorized access to CHUM control endpoints"

      - alert: CHUMAPILatencyHigh
        expr: histogram_quantile(0.95, rate(chum_api_request_duration_seconds_bucket[5m])) > 2
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "CHUM API p95 latency exceeds 2 seconds"

      - alert: CHUMSchedulerPaused
        expr: chum_scheduler_status == 0
        for: 30m
        labels:
          severity: warning
        annotations:
          summary: "CHUM scheduler has been paused for 30+ minutes"
```

### Log Rotation

Configure logrotate for the audit log:

```
# /etc/logrotate.d/chum
/var/log/chum/api-audit.log {
    daily
    rotate 90
    compress
    delaycompress
    missingok
    notifempty
    create 0640 chum chum
    postrotate
        systemctl reload chum 2>/dev/null || true
    endscript
}
```

## Security Considerations

1. **Token Security**: Use long, random tokens (32+ chars recommended)
2. **Transport Security**: Use TLS in production (reverse proxy recommended)
3. **Network Security**: Restrict network access to API port via firewall
4. **Audit Retention**: Rotate audit logs and comply with retention policies
5. **Monitoring**: Alert on authentication failures and unexpected usage patterns

## Troubleshooting

### Authentication Not Working

Check configuration:
```bash
# Verify config syntax
chum -config chum.toml -dry-run

# Check logs for auth errors
journalctl -u chum -f | grep auth
```

### Local-Only Mode Issues

Verify client IP classification:
- `127.0.0.1` - Always local
- `::1` - Always local  
- RFC 1918 ranges (`10.x`, `172.16-31.x`, `192.168.x`) - Considered local
- Other IPs - Considered remote

### Audit Log Not Writing

Check permissions and directory:
```bash
# Ensure directory exists and is writable
sudo mkdir -p /var/log/chum
sudo chown chum:chum /var/log/chum
sudo chmod 755 /var/log/chum
```