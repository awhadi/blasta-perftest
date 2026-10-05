package ws

import "crypto/tls"

func tlsConfig(insecure bool) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure}
}
