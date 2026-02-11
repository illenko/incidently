# Example: "Problems with Apple Pay"

An imaginary end-to-end scenario showing how Incidently handles a focused investigation.

## Setup

```yaml
# config/config.yaml

mcp_servers:
  - name: grafana
    url: "https://grafana-mcp.internal/sse"
  - name: gcp-logging
    url: "https://gcp-logging-mcp.internal/sse"

coordinator:
  model: gemini-2.0-flash
  description: "Understands operator requests, picks relevant playbooks, delegates to specialists, aggregates results"
  instruction: "instructions/coordinator.md"
  temperature: 0.1

agents:
  - name: metrics-analyst
    model: gemini-2.5-pro
    description: "Queries Grafana dashboards and interprets metrics data"
    instruction: "instructions/metrics-analyst.md"
    temperature: 0.2
    tools: [grafana]

  - name: log-analyst
    model: gemini-2.5-pro
    description: "Searches and analyzes application logs"
    instruction: "instructions/log-analyst.md"
    temperature: 0.2
    tools: [gcp-logging]

playbooks_dir: "playbooks/"
```

Playbooks loaded at startup:

```
payment-investigation  — "Payment service analysis — success rates, gateway errors"
                          tags: [payments, apple-pay, google-pay, checkout]
health-check           — "Broad system health check — metrics, logs, infrastructure"
                          tags: [health, overview, system]
dependency-analysis    — "External dependency health and error breakdown"
                          tags: [dependencies, external-services, gateways, timeouts]
infra-check            — "Infrastructure health — pods, queues, databases"
                          tags: [infrastructure, pods, queues, database]
deployment-check       — "Recent deployment impact analysis"
                          tags: [deployment, rollout, canary, rollback]
```

## Interaction Diagram

```
Operator                Slack Gateway          Coordinator              metrics-analyst         log-analyst
   │                         │                (gemini-2.0-flash)        (gemini-2.5-pro)       (gemini-2.5-pro)
   │                         │                      │                        │                       │
   │  @bot problems with     │                      │                        │                       │
   │  apple pay              │                      │                        │                       │
   │────────────────────────>│                      │                        │                       │
   │                         │                      │                        │                       │
   │  ⏳ Investigating...    │                      │                        │                       │
   │<────────────────────────│                      │                        │                       │
   │                         │                      │                        │                       │
   │                         │  "problems with      │                        │                       │
   │                         │   apple pay"         │                        │                       │
   │                         │─────────────────────>│                        │                       │
   │                         │                      │                        │                       │
   │                         │               ┌──────┴──────┐                 │                       │
   │                         │               │ READ INDEX  │                 │                       │
   │                         │               │             │                 │                       │
   │                         │               │ payment-inv │                 │                       │
   │                         │               │  tags match │                 │                       │
   │                         │               │ dependency  │                 │                       │
   │                         │               │  tags match │                 │                       │
   │                         │               │             │                 │                       │
   │                         │               │ health ✗    │                 │                       │
   │                         │               │ infra ✗     │                 │                       │
   │                         │               │ deploy ✗    │                 │                       │
   │                         │               └──────┬──────┘                 │                       │
   │                         │                      │                        │                       │
   │                         │               get_playbook                    │                       │
   │                         │               ("payment-investigation")       │                       │
   │                         │               ──────────────────>             │                       │
   │                         │               <── full content ──             │                       │
   │                         │                      │                        │                       │
   │                         │               get_playbook                    │                       │
   │                         │               ("dependency-analysis")         │                       │
   │                         │               ──────────────────>             │                       │
   │                         │               <── full content ──             │                       │
   │                         │                      │                        │                       │
   │                         │               ┌──────┴──────┐                 │                       │
   │                         │               │ PLAN STEPS  │                 │                       │
   │                         │               │             │                 │                       │
   │                         │               │ 1. payment  │                 │                       │
   │                         │               │    metrics  │                 │                       │
   │                         │               │    → metrics│                 │                       │
   │                         │               │             │                 │                       │
   │                         │               │ 2. payment  │                 │                       │
   │                         │               │    logs     │                 │                       │
   │                         │               │    → log    │                 │                       │
   │                         │               │             │                 │                       │
   │                         │               │ 3. gateway  │                 │                       │
   │                         │               │    health   │                 │                       │
   │                         │               │    → metrics│                 │                       │
   │                         │               └──────┬──────┘                 │                       │
   │                         │                      │                        │                       │
   │                         │                      │ DELEGATE: check        │                       │
   │                         │                      │ Payments dashboard,    │                       │
   │                         │                      │ success rate per       │                       │
   │                         │                      │ payment method,        │                       │
   │                         │                      │ last 15 min, compare   │                       │
   │                         │                      │ to last week           │                       │
   │                         │                      │───────────────────────>│                       │
   │                         │                      │                        │                       │
   │  📊 Checking payment    │                      │                 ┌──────┴──────┐                │
   │  metrics...             │                      │                 │ grafana MCP │                │
   │<────────────────────────│                      │                 │ query_dash  │                │
   │                         │                      │                 │ ("Payments")│                │
   │                         │                      │                 └──────┬──────┘                │
   │                         │                      │                        │                       │
   │                         │                      │  Apple Pay: 73%        │                       │
   │                         │                      │  (baseline 99.2%)      │                       │
   │                         │                      │  Google Pay: 99.1% ✅  │                       │
   │                         │                      │  Card: 99.4% ✅        │                       │
   │                         │                      │  Latency p99: 4.2s     │                       │
   │                         │                      │  (baseline 1.1s)       │                       │
   │                         │                      │<───────────────────────│                       │
   │                         │                      │                        │                       │
   │                         │                      │ DELEGATE: search       │                       │
   │                         │                      │ payment-service logs,  │                       │
   │                         │                      │ severity >= WARN,      │                       │
   │                         │                      │ last 15 min, group by  │                       │
   │                         │                      │ payment method         │                       │
   │                         │                      │───────────────────────────────────────────────>│
   │                         │                      │                        │                       │
   │  📋 Searching payment   │                      │                        │                ┌──────┴──────┐
   │  logs...                │                      │                        │                │gcp-log MCP  │
   │<────────────────────────│                      │                        │                │search(svc=  │
   │                         │                      │                        │                │payment,sev  │
   │                         │                      │                        │                │>=WARN)      │
   │                         │                      │                        │                └──────┬──────┘
   │                         │                      │                        │                       │
   │                         │                      │  312x "Apple Pay token │                       │
   │                         │                      │  validation timeout"   │                       │
   │                         │                      │  since 14:15 UTC       │                       │
   │                         │                      │  endpoint: /api/pay/   │                       │
   │                         │                      │  apple-pay/validate    │                       │
   │                         │                      │  no other payment      │                       │
   │                         │                      │  method affected       │                       │
   │                         │                      │<─────────────────────────────────────────────── │
   │                         │                      │                        │                       │
   │                         │                      │ DELEGATE: check        │                       │
   │                         │                      │ Payment Gateways       │                       │
   │                         │                      │ dashboard, response    │                       │
   │                         │                      │ times + error rates    │                       │
   │                         │                      │ per provider           │                       │
   │                         │                      │───────────────────────>│                       │
   │                         │                      │                        │                       │
   │  📊 Checking gateway    │                      │                 ┌──────┴──────┐                │
   │  health...              │                      │                 │ grafana MCP │                │
   │<────────────────────────│                      │                 │ query_dash  │                │
   │                         │                      │                 │ ("Payment   │                │
   │                         │                      │                 │  Gateways") │                │
   │                         │                      │                 └──────┬──────┘                │
   │                         │                      │                        │                       │
   │                         │                      │  Apple gateway:        │                       │
   │                         │                      │  resp time 8.2s        │                       │
   │                         │                      │  (baseline 0.3s)       │                       │
   │                         │                      │  error rate: 26%       │                       │
   │                         │                      │  Others: normal        │                       │
   │                         │                      │<───────────────────────│                       │
   │                         │                      │                        │                       │
   │                         │               ┌──────┴──────┐                 │                       │
   │                         │               │ AGGREGATE   │                 │                       │
   │                         │               │             │                 │                       │
   │                         │               │ combine all │                 │                       │
   │                         │               │ findings    │                 │                       │
   │                         │               │ into final  │                 │                       │
   │                         │               │ summary per │                 │                       │
   │                         │               │ playbook    │                 │                       │
   │                         │               │ output fmt  │                 │                       │
   │                         │               └──────┬──────┘                 │                       │
   │                         │                      │                        │                       │
   │                         │  final summary       │                        │                       │
   │                         │<─────────────────────│                        │                       │
   │                         │                      │                        │                       │
   │  🔍 Apple Pay           │                      │                        │                       │
   │  Investigation          │                      │                        │                       │
   │  ...                    │                      │                        │                       │
   │<────────────────────────│                      │                        │                       │
   │                         │                      │                        │                       │
```

## What the Operator Sees in Slack

### Initial investigation

```
Operator: @incidently problems with apple pay

Bot:      ⏳ Investigating...
Bot:      📊 Checking payment metrics...
Bot:      📋 Searching payment logs...
Bot:      📊 Checking gateway health...

Bot:      🔍 Apple Pay Investigation — 14:31 UTC

          Payment Methods
          🔴 Apple Pay success rate: 73% (baseline 99.2%)
          🔴 Apple Pay latency p99: 4.2s (baseline 1.1s)
          ✅ Google Pay: 99.1%
          ✅ Card: 99.4%

          Logs
          ❌ "Apple Pay token validation timeout" — 312 occurrences
          ❌ Started at 14:15 UTC
          ❌ Affected endpoint: /api/payments/apple-pay/validate
          ✅ No other payment methods affected

          Gateway
          🔴 Apple Pay gateway response time: 8.2s (baseline 0.3s)
          🔴 Apple Pay gateway error rate: 26%
          ✅ Other gateways: normal

          Root cause: Apple Pay gateway is responding slowly (8.2s vs
          0.3s baseline), causing token validation timeouts on our side.
          Only Apple Pay is affected — all other payment methods are healthy.

          Suggest:
          - Check Apple's system status page
          - Consider temporarily increasing timeout threshold
          - Monitor if gateway response time improves
```

### Follow-up in thread

```
Operator: when exactly did it start and is it getting worse?

Bot:      ⏳ Analyzing trend...
Bot:      📊 Pulling timeline data...

Bot:      📈 Apple Pay Timeline

          14:00 — gateway resp: 0.3s, success: 99.2% (normal)
          14:15 — gateway resp: 2.1s, success: 91.3% (degradation starts)
          14:20 — gateway resp: 5.8s, success: 78.4% (escalating)
          14:25 — gateway resp: 8.2s, success: 73.0% (current)

          Trend: getting worse. Gateway response time doubled in last
          10 minutes (5.8s → 8.2s). Success rate still declining.

          Suggest: this looks like it's escalating, not a transient blip.
          Consider alerting the on-call team and preparing a customer
          communication if it continues.
```

### Pivoting to a different angle

```
Operator: are any other external dependencies affected?

Bot:      ⏳ Checking dependencies...
Bot:      📊 Querying dependency overview...

Bot:      🔗 External Dependencies — 14:35 UTC

          ✅ Google Pay gateway: 99.8%
          ✅ Stripe: 99.9%
          🔴 Apple Pay gateway: 73% (already known)
          ✅ Address validation service: 99.7%
          ✅ Fraud detection API: 99.5%

          Only Apple Pay gateway is degraded. No broader dependency issue.
```

## What Happened Behind the Scenes

```
Step    Coordinator                   Agent            MCP Tool            Tokens used
─────   ─────────────────────────     ──────────────   ──────────────      ───────────
  1     Read playbook index           —                —                   ~1,500
        (5 playbooks × ~50 tokens)

  2     get_playbook                  —                —                   ~800
        ("payment-investigation")

  3     get_playbook                  —                —                   ~600
        ("dependency-analysis")

  4     Delegate: payment metrics     metrics-analyst  grafana.query       ~2,000
                                                       ("Payments")

  5     Delegate: payment logs        log-analyst      gcp.search          ~2,500
                                                       (payment-service)

  6     Delegate: gateway health      metrics-analyst  grafana.query       ~1,500
                                                       ("Payment Gateways")

  7     Aggregate + format            —                —                   ~1,000
        final summary

Total: ~10,000 tokens, 3 MCP tool calls, 2 playbooks loaded (3 skipped)
```

If the operator had asked "how's the system doing?" instead, the coordinator would have loaded
`health-check` (and possibly `infra-check`) but skipped `payment-investigation`,
`dependency-analysis`, and `deployment-check` — different playbooks, same engine.
