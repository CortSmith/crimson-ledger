// Package awswire is the wiring of the two Lambda functions (T10): cold-start initialisation, the pepper
// read from SSM, the admin command handler. Keeping it here keeps the two main.go files tiny and testable with fakes.
package awswire
