---
description: "Investigate payment failures, errors, and transaction issues"
tags: [payments, failed, errors, transactions]
---

# Payment Investigation

## Payment Metrics

Query the `eelnuuxr4smbka/tech` dashboard (folder: "System Monitoring", name: "Tech").

Pull these panels for the last 15 minutes:
- Payment error rate
- Payment success rate
- Transaction throughput

Compare current values to the same time window last week to establish a baseline.

## Payment Logs

Search application logs for the last 15 minutes using these queries:
- Search for `failed` in payment-related logs
- Search for `error` in payment-related logs

Filter for payment-related services and components. Focus on severity >= WARN.

Identify:
- Top error messages by frequency
- Affected services and components
- Any new error patterns not seen in the baseline period

## Output Format

Group findings by error type:

1. **Error type / pattern** — brief description
   - Frequency: how often it occurs
   - Affected services: which services are impacted
   - Severity: normal / warning / critical
   - Evidence: key metrics and log excerpts

2. Repeat for each distinct error type.

**Overall severity:** normal / warning / critical

**Suggested next steps:**
- Specific actions based on findings (e.g. check upstream dependency, review recent deployment, scale service)
- Escalation guidance if severity is critical
