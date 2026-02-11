---
description: "Broad system health check — metrics, logs, infrastructure"
tags: [health, overview, system]
---

# Health Check

## Application Metrics

Query the "Application Overview" dashboard. Pull these panels for the last 15 minutes:
- Error rate
- Latency p99
- Throughput (rps)

Compare to the same time window last week.

## Recent Logs

Search application logs for the last 15 minutes.
Filter: severity >= WARN.

Identify:
- Top error messages by frequency
- Any new error patterns not seen last week

## Output format

Present a brief summary: all clear, or list anomalies with severity.