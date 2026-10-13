// Package quota will enforce per-user daily limits for paid OCR fallback.
package quota

// TODO: Define a quota interface and atomically enforce the daily page limit.
// TODO: Use USER#<id> / QUOTA#<yyyy-mm-dd> items with TTL.
// TODO: Keep Textract fallback disabled until quota enforcement is implemented.
