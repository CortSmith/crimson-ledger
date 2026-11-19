package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// isLoopbackAddr reports whether a host:port listens on this machine only. An empty host
// (":8443") means every interface and is therefore not loopback.
func isLoopbackAddr(hostport string) (bool, error) {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false, fmt.Errorf("listen address %q must look like host:port: %w", hostport, err)
	}
	if host == "localhost" {
		return true, nil
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false, nil // a DNS name other than localhost may resolve to anything
	}
	return ip.IsLoopback(), nil
}

// errPublicBind explains the PLAN.md section 7 binding rule.
var errPublicBind = errors.New("refusing to listen beyond this machine without TLS: " +
	"run 'crimson-api tls-selfsign --host <your LAN address>' and set tls_cert/tls_key in api.json, " +
	"or pass --insecure-lan to accept plain HTTP on your network")

// bindMode says how the listener is exposed; it is printed in the startup banner.
type bindMode string

const (
	modeLoopback    bindMode = "loopback only, plain http"
	modeTLS         bindMode = "TLS"
	modeInsecureLAN bindMode = "INSECURE: plain http beyond this machine"
	modeContainer   bindMode = "container: plain http inside the container, the host publish decides exposure"
)

// ContainerEnv marks a process that runs inside a container whose port is published by the container runtime
// (docker compose publishes it on the host loopback only). Only then may a non-loopback address be plain http.
const ContainerEnv = "CRIMSON_LEDGER_BEHIND_CONTAINER"

// chooseBindMode applies the binding rules to a listen address.
func chooseBindMode(listen string, hasTLS, insecureLAN, behindContainer bool) (bindMode, error) {
	loopback, err := isLoopbackAddr(listen)
	switch {
	case err != nil:
		return "", err
	case hasTLS:
		return modeTLS, nil
	case loopback:
		return modeLoopback, nil
	case insecureLAN:
		return modeInsecureLAN, nil
	case behindContainer:
		return modeContainer, nil
	}
	return "", errPublicBind
}
