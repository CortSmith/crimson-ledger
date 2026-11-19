// Package apiserver is the Crimson Ledger HTTP API: one http.Handler shared by the local server
// and the AWS Lambda adapter. It owns routing, API-key authentication, rate limits, the error
// shape and security headers. Business handlers (ingest, reads) are rows in the route table.
package apiserver
