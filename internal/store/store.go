// Package store will persist jobs, step results, and extracted data in DynamoDB.
package store

// TODO: Define a small JobStore interface with context.Context as the first argument.
// TODO: Implement the single-table keys documented in AGENTS.md without Scan.
// TODO: Use conditional writes for status transitions and jobId for idempotency.
// TODO: Set a seven-day TTL and cover access patterns with DynamoDB Local integration tests.
