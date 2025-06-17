### Record Synchronization System Design  
**Component Implemented:** Sync Worker (System A → System B)  
**Tech Stack:** Go, Redis, Kafka  

---

### 1. Problem Scope  
Designed a **provider-specific sync worker** handling:  
- Schema transformation  
- Rate-limited API calls  
- Retry logic with exponential backoff  + Idempotency Key
- Dead-letter queue for failures  
- Concurrent processing within external api rate limits  

---

### 2. High-Level Design  
![alt text](image.png)

---

### 3. Key Components  

| **Component**         | **Responsibility**                                  | **Key Mechanism**                     |
|-----------------------|----------------------------------------------------|---------------------------------------|
| **Worker Pool**       | Concurrent processing per External Client provider             | Provider-specific goroutine pools     |
| **Schema Transformer**| Map internal→external schemas                      | Config-driven field mappings          |
| **Rate Limiter**      | Enforce External API quotas                             | Redis token buckets per provider      |
| **External API Client**    | Idempotent sync operations                         | Exponential backoff + Idempotency Key|
| **DLQ Handler**       | Persist failed syncs for reprocessing              | Kafka topic with error context        |

---

### 4. Design Trade-offs  

| **Decision**               | **Alternative**       | **Rationale**                                     |
|----------------------------|-----------------------|---------------------------------------------------|
| Provider-specific workers  | Global worker pool    | Prevents one External clinet from starving others             |
| Redis rate limiting        | In-memory limiter     | Supports horizontal scaling                       |
| Kafka DLQ                  | Database storage      | Enables bulk replay and stream processing         |
| 3 retry attempts           | Infinite retries      | Balances delivery guarantee vs. resource usage    |

---

### 5. Scalability Approach  
- **300M req/day target:**  
  ```go
  workers = (rate_limit_rps * 1.5)  // Sizing formula or can be adjusted based on API Limiatations
  ```
  - Example: External API With @ 10 RPS → 15 workers  
- **Horizontally scalable:** Add pods per External Service provider  
- **Throughput:** 3,472+ req/sec achievable with cluster scaling  

---

### 6. Failure Handling  
- **Transient errors:** Jittered backoff (up to 3 retries)  
- **Permanent errors:** DLQ write with original payload + error  
- **System faults:** Context cancellation for graceful shutdown  

---

### 7. How to Run  
```bash
# 1. Start Redis and Kafka
docker-compose up -d

# 2. Set config (config.json)
{
  "providers": {
    "service_a": {
      "rate_limit_rps": 10,
      "max_workers": 15,
      "field_mapping": [...]
    }
  }
}

# 3. Start worker
go run main.go --config=config.json
```

**Metrics Endpoint:** `http://localhost:9090/metrics`  
**DLQ Topic:** `sync_dlq`  

---

### 8. Production Enhancements  
- **Observability:** Prometheus metrics + Grafana dashboard  
- **Secrets:** Vault integration for credentials  
- **Auto-scaling:** K8s HPA based on queue backlog  