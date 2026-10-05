package main

import "net"

func netSplitHostPort(addr string) (string, string, error) {
	return net.SplitHostPort(addr)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
