---
description: "Comprehensive system health check across all monitored services"
tags: [health, monitoring, system, status, check]
---

# System Health Check

## What to do

Perform a full system health check:
- Query all relevant data sources for the last 30 minutes
- Compare current values against the same time window one week ago
- Classify each area as normal / warning / critical

## What to pass

- The current time (from the user message)
- Request: "Perform a full system health check"

## Output Format

Focus on problems. Only report areas with **warning** or **critical** status. If everything is normal, say so briefly.

If the overall status is **warning** or **critical**, highlight the affected areas at the top of the report for quick identification.